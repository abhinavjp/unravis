// Rebuilds the generated web files for a promotion and commits them to the promote branch.
// Called by Promote-Develop.ps1 (docs/pipeline/dist-on-promotion.md). Pull requests into develop do not commit
// web/dist, cmd/unravis-setup/ui and cmd/unravis-lite/ui; the promotion does, once.
//
//   node tools/release/promote-dist.mjs --repo owner/name --branch promote/1a2b3c4 [--node-version 22.23.5] [--no-push] [--keep]
//
// What it does, in order:
//   1. Stops unless this Node is exactly the pinned version (CI builds with the same one).
//   2. Clones the branch into a temporary folder: the BUILD clone. Dependency code runs only there (it can write anywhere in it, .git
//      included, and next to it).
//   3. npm ci and npm run build in web of the build clone, in a scrubbed environment (buildEnv); the three generated folders are copied
//      out (plain files only: a link or a .git entry stops the run). Then web/node_modules is deleted, npm ci and the build run again and
//      the folders are copied again; the two copies must be byte for byte equal (a build that is not reproducible stops here, nothing
//      pushed; the Windows runner's check-dist on the promotion pull request is the proof across machines).
//   4. The build clone and everything dependency code could write to are DELETED. Only then is the COMMIT clone made, in a new folder:
//      a fresh clone of the branch (no templates), whose local git config, hooks and info/attributes are checked to hold nothing a
//      build could have added; the check is repeated right before the push (a process the build left running could plant meanwhile). The copied folders are
//      put into it, node web/scripts/check-dist.cjs is run there (same bytes as committed: nothing to do; different: the three folders
//      are committed with the subject "Rebuild web files for promotion <branch>", which ReleaseLib.ps1 knows).
//   5. Pushes the branch from the commit clone (a plain push, never a force). Only clone and push carry the token (one header, in
//      their environment); every git command runs with hooks and core.fsmonitor off, without GIT_DIR and the like and without any
//      GH_TOKEN-like variable from the caller's environment.
// Last line printed: DIST_COMMIT=<sha> or DIST_COMMIT=none. Promote-Develop.ps1 requires that line and stops without it. Exit 0 ok, 1 build or check failed, 2 cannot run.
//
// Test hooks (used by tools/ci/test-promote-dist.mjs only): --remote <url or path> replaces https://github.com/<repo>.git,
// --install-cmd and --build-cmd replace `npm ci` and `npm run build`.

import { spawnSync } from 'node:child_process';
import { chmodSync, copyFileSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { devNull, tmpdir } from 'node:os';
import { delimiter, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

// Keep equal to the node-version in .github/workflows (ci.yml web job, release.yml, fast-gate.yml). tools/ci/test-promote-dist.mjs checks it.
export const NODE_VERSION = '22.23.5';
export const ROOTS = ['web/dist', 'cmd/unravis-setup/ui', 'cmd/unravis-lite/ui'];
export const COMMIT_SUBJECT = 'Rebuild web files for promotion';

// Environment names the install and the build keep. Everything else is dropped (an allow list, not a deny list: a token variable
// nobody thought of is not passed on). Windows names are matched case-insensitively.
const KEEP = /^(PATH|PATHEXT|SYSTEMROOT|WINDIR|COMSPEC|SYSTEMDRIVE|OS|TEMP|TMP|TMPDIR|LANG|LANGUAGE|LC_[A-Z]+|TZ|TERM|NO_COLOR|FORCE_COLOR|PROGRAMFILES|PROGRAMFILES\(X86\)|PROGRAMW6432|COMMONPROGRAMFILES|PROGRAMDATA|ALLUSERSPROFILE|PROCESSOR_ARCHITECTURE|NUMBER_OF_PROCESSORS|HTTP_PROXY|HTTPS_PROXY|NO_PROXY|ALL_PROXY|SSL_CERT_FILE|SSL_CERT_DIR|NODE_EXTRA_CA_CERTS)$/i;
// npm settings that point npm at a registry or a certificate (not the auth settings: npm_config__authtoken and the like stay out).
const NPM_KEEP = /^npm_config_(registry|cafile|strict_ssl|proxy|https_proxy|noproxy)$/i;
// A kept variable whose value looks like a GitHub token is dropped too (a token pasted into a proxy or certificate variable).
const TOKEN_VALUE = /(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})/;

/**
 * The environment `npm ci` and `npm run build` run in (they run dependency code, so they get nothing that could reach GitHub):
 *  - only the variables in KEEP, no GH_TOKEN, GITHUB_TOKEN, GH_ENTERPRISE_TOKEN, npm_config_* (registry tokens), NODE_AUTH_TOKEN, ...;
 *  - a private, empty HOME (and APPDATA, XDG_*, npm cache and npm rc files) under `scratch`, so no ~/.gitconfig, ~/.npmrc, gh login
 *    (GH_CONFIG_DIR is that empty folder, so `gh auth token` finds nothing) or credential store of the person running this is read;
 *  - git: no system config, an empty global config, no credential helper (GIT_CONFIG_COUNT/KEY/VALUE reset the helper list), no prompt;
 *  - a `gh` stub first on PATH that only fails, so a dependency script that runs `gh auth token` gets no token.
 * This lowers the risk; it is NOT a security boundary. Dependency code runs as the same user as this script, so it can still read this
 * process's environment (Linux: /proc/<parent pid>/environ; Windows: ReadProcessMemory), call a program by its absolute path (a gh.exe
 * that reads its login from the operating system's keyring even with an empty GH_CONFIG_DIR), or ask the OS credential store directly.
 * What protects the result is that the lock file pins the dependencies, that check-dist compares the bytes, and the ORDER in main():
 * the build clone is deleted before the commit clone exists and only copies of the three generated folders cross over, so the
 * build's own writes never reach the commit clone. A process the build left running (a detached child) could still wait for the commit
 * clone and plant something in it, so: git commands that do not push get no token (neither GH_TOKEN nor the header), and the commit
 * clone's config, hooks and info/attributes are checked again right before the push, the only command that carries the token.
 * Settings in the person's own ~/.npmrc are not read (HOME is private): a registry mirror or CA must come in as npm_config_registry,
 * npm_config_cafile, HTTPS_PROXY, NODE_EXTRA_CA_CERTS or SSL_CERT_FILE, which are kept when they carry no password. The npm cache is
 * private and empty, so every promotion downloads every package.
 * Creates folders and files under `scratch`; returns the environment object.
 */
export function buildEnv(env, scratch) {
  const out = {};
  for (const [k, v] of Object.entries(env)) {
    if (typeof v !== 'string' || TOKEN_VALUE.test(v)) continue;
    if (KEEP.test(k)) out[k] = v;
    // npm settings a registry mirror or a company certificate need, kept only when the value carries no password (no user:pass@host).
    else if (NPM_KEEP.test(k) && !/:\/\/[^/]*@/.test(v)) out[k] = v;
  }
  const home = join(scratch, 'home'), cfg = join(scratch, 'gh-config'), bin = join(scratch, 'bin'), cache = join(scratch, 'cache');
  for (const d of [home, cfg, bin, cache, join(home, 'AppData', 'Roaming'), join(home, 'AppData', 'Local')]) mkdirSync(d, { recursive: true });
  // Three DIFFERENT empty files: npm 10 stops with "double-loading config" when the user and the global config are the same file.
  const emptyUser = join(scratch, 'empty-user.npmrc'), emptyGlobal = join(scratch, 'empty-global.npmrc'), emptyGit = join(scratch, 'empty-git.cfg');
  for (const f of [emptyUser, emptyGlobal, emptyGit]) writeFileSync(f, '');
  // `gh` that cannot give a token: a shell script (found first on POSIX) and a .cmd (found first on Windows, where PATHEXT lists .CMD).
  writeFileSync(join(bin, 'gh'), '#!/bin/sh\necho "gh is not available in the web build" >&2\nexit 1\n');
  chmodSync(join(bin, 'gh'), 0o755);
  writeFileSync(join(bin, 'gh.cmd'), '@echo gh is not available in the web build 1>&2\r\n@exit /b 1\r\n');
  const pathKey = Object.keys(out).find((k) => k.toUpperCase() === 'PATH') || 'PATH';
  out[pathKey] = bin + delimiter + (out[pathKey] || '');
  Object.assign(out, {
    HOME: home, USERPROFILE: home, APPDATA: join(home, 'AppData', 'Roaming'), LOCALAPPDATA: join(home, 'AppData', 'Local'),
    XDG_CONFIG_HOME: join(home, '.config'), XDG_CACHE_HOME: join(home, '.cache'), XDG_DATA_HOME: join(home, '.local', 'share'),
    GH_CONFIG_DIR: cfg, GH_PROMPT_DISABLED: '1', GH_NO_UPDATE_NOTIFIER: '1',
    npm_config_cache: cache, npm_config_userconfig: emptyUser, npm_config_globalconfig: emptyGlobal, npm_config_update_notifier: 'false',
    GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: emptyGit, GIT_TERMINAL_PROMPT: '0', GCM_INTERACTIVE: 'never',
    GIT_CONFIG_COUNT: '1', GIT_CONFIG_KEY_0: 'credential.helper', GIT_CONFIG_VALUE_0: '',
  });
  return out;
}

class Stop extends Error {
  constructor(code, message) {
    super(message);
    this.code = code;
  }
}

/**
 * Copies the generated folders (ROOTS) from the build clone into the commit clone, replacing what the commit clone has there.
 * Only regular files and folders are copied. A link, a device, a name that is `.git` or the file with a NUL/newline in it stops the run:
 * build code could use them to reach outside the folder or to plant git data in the commit clone.
 */
const lstatOrNull = (p) => { try { return lstatSync(p); } catch { return null; } };
export function copyRoots(from, to) {
  const bad = (rel, why) => new Stop(1, 'The build produced ' + why + ' in ' + rel + '. Nothing was pushed.');
  const copy = (src, dst, rel) => {
    const st = lstatSync(src);
    if (st.isSymbolicLink()) throw bad(rel, 'a link');
    if (st.isDirectory()) {
      mkdirSync(dst, { recursive: true });
      for (const name of readdirSync(src)) {
        if (name.toLowerCase() === '.git' || /[\0\r\n]/.test(name)) throw bad(rel + '/' + name, 'a name that is not allowed');
        copy(join(src, name), join(dst, name), rel + '/' + name);
      }
    } else if (st.isFile()) copyFileSync(src, dst);
    else throw bad(rel, 'something that is not a file');
  };
  for (const root of ROOTS) {
    rmSync(join(to, root), { recursive: true, force: true, maxRetries: 3 });
    if (lstatOrNull(join(from, root))) copy(join(from, root), join(to, root), root);
  }
}

/** True when the ROOTS under `a` and `b` hold the same files with the same bytes (names compared as written). */
export function sameRoots(a, b) {
  const list = (base) => {
    const out = new Map();
    const walk = (rel) => {
      const st = lstatOrNull(join(base, rel));
      if (!st) return;
      if (st.isDirectory()) for (const n of readdirSync(join(base, rel)).sort()) walk(rel + '/' + n);
      else out.set(rel, readFileSync(join(base, rel)));
    };
    for (const r of ROOTS) walk(r);
    return out;
  };
  const x = list(a), y = list(b);
  if (x.size !== y.size) return false;
  for (const [k, v] of x) if (!y.has(k) || !v.equals(y.get(k))) return false;
  return true;
}

function parseArgs(argv) {
  const o = { repo: '', branch: '', node: NODE_VERSION, push: true, keep: false, remote: '', install: 'npm ci', build: 'npm run build' };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const val = () => {
      if (i + 1 >= argv.length) throw new Stop(2, 'missing value after ' + a);
      return argv[++i];
    };
    if (a === '--repo') o.repo = val();
    else if (a === '--branch') o.branch = val();
    else if (a === '--node-version') o.node = val();
    else if (a === '--remote') o.remote = val();
    else if (a === '--install-cmd') o.install = val();
    else if (a === '--build-cmd') o.build = val();
    else if (a === '--no-push') o.push = false;
    else if (a === '--keep') o.keep = true;
    else throw new Stop(2, 'unknown argument ' + a);
  }
  if (!o.branch) throw new Stop(2, 'usage: node promote-dist.mjs --repo owner/name --branch promote/<sha7> [--no-push]');
  if (!o.remote && !/^[\w.-]+\/[\w.-]+$/.test(o.repo)) throw new Stop(2, '--repo must look like owner/name');
  return o;
}

function run(cmd, args, opts = {}) {
  const r = spawnSync(cmd, args, {
    encoding: 'utf8',
    maxBuffer: 256 * 1024 * 1024,
    shell: opts.shell ?? false,
    cwd: opts.cwd,
    env: opts.env ?? process.env,
    stdio: opts.show ? ['ignore', 'inherit', 'inherit'] : ['ignore', 'pipe', 'pipe'],
  });
  if (r.error) throw new Stop(2, cmd + ' could not be started: ' + r.error.message);
  return r;
}

// Environment for git commands: the caller's, without the variables that send git to another repository or make it run another program
// (GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE, GIT_SSH_COMMAND, GIT_ASKPASS, GIT_CONFIG_COUNT and its KEY/VALUE, ...), without variables that
// carry a secret, and without replace refs. GIT_SSL_CAINFO and the like are kept (a PC may need them to reach github.com).
const GIT_REDIRECTS = /^GIT_(DIR|WORK_TREE|INDEX_FILE|OBJECT_DIRECTORY|ALTERNATE_OBJECT_DIRECTORIES|COMMON_DIR|NAMESPACE|PREFIX|SSH|SSH_COMMAND|SSH_VARIANT|PROXY_COMMAND|ASKPASS|EXTERNAL_DIFF|PAGER|EDITOR|SEQUENCE_EDITOR|CONFIG_COUNT|CONFIG_KEY_\d+|CONFIG_VALUE_\d+|CONFIG_PARAMETERS|EXEC_PATH|TEMPLATE_DIR|DEFAULT_REF_FORMAT|REPLACE_REF_BASE|TRACE\w*|CURL_VERBOSE)$/i;
// Names that carry a secret (GH_TOKEN, GITHUB_TOKEN, NPM_TOKEN, ...): git commands that do not push never get them, so a filter or hook
// that something plants in the commit clone cannot read one. Clone and push get the token only as the one header in gitEnv.
const SECRET_NAME = /(TOKEN|SECRET|PASSWORD|PASSPHRASE)/i;
const NOT_A_SECRET = /^GIT_SSL_CERT_PASSWORD_PROTECTED$/i; // a yes/no flag, not a password
export function gitBaseEnv(env) {
  const out = {};
  for (const [k, v] of Object.entries(env)) if (!GIT_REDIRECTS.test(k) && (!SECRET_NAME.test(k) || NOT_A_SECRET.test(k))) out[k] = v;
  out.GIT_NO_REPLACE_OBJECTS = '1';
  out.GIT_TERMINAL_PROMPT = '0';
  return out;
}

/**
 * Throws unless `dir` is a fresh clone with nothing added since it was made. An ALLOW list, not a deny list: the local config may hold
 * only what `git clone --single-branch --depth 1` writes (core.repositoryformatversion/filemode/bare/logallrefupdates/symlinks/ignorecase/
 * precomposeunicode, remote.origin.url and .fetch, branch.<name>.remote and .merge), each at most once; anything else (a filter, a hook
 * path, fsmonitor, a proxy, an include, a url rewrite, a pushurl, a receivepack, ...) stops the run. With `remote` and `branch` given the
 * values must be exactly what the clone of that branch from that URL writes. Also: no hook that is not a sample and no
 * .git/info/attributes. Run after the clone and again right before the push.
 */
export function assertFreshClone(dir, { remote = '', branch = '', env = gitBaseEnv(process.env) } = {}) {
  // Fail closed: when git cannot read the clone's config at all (a planted extension such as extensions.refstorage on a version 0
  // repository makes git 2.50+ stop with "fatal: --local can only be used inside a git repository"), that is a refusal with this
  // wording, not git's own text.
  const read = git(dir, ['config', '--local', '--list'], { env, allowFail: true });
  if (read.status !== 0) throw new Stop(1, 'The fresh clone has git settings that a fresh clone does not have (git cannot read its local config). Nothing was pushed.');
  const lines = String(read.stdout || '').split('\n').map((l) => l.replace(/\r$/, '')).filter(Boolean);
  const seen = new Set();
  const bad = [];
  for (const line of lines) {
    const i = line.indexOf('=');
    const key = i < 0 ? line : line.slice(0, i), value = i < 0 ? '' : line.slice(i + 1);
    let ok = false;
    const m = /^branch\.(.+)\.(remote|merge)$/.exec(key);
    if (/^core\.(repositoryformatversion|filemode|bare|logallrefupdates|symlinks|ignorecase|precomposeunicode)$/.test(key)) {
      ok = key === 'core.bare' ? value === 'false' : key === 'core.repositoryformatversion' ? value === '0' : true;
    } else if (key === 'remote.origin.url') ok = remote === '' || value === remote;
    else if (key === 'remote.origin.fetch') ok = /^\+refs\/heads\/[^:]+:refs\/remotes\/origin\/[^:]+$/.test(value) && (branch === '' || value === '+refs/heads/' + branch + ':refs/remotes/origin/' + branch);
    else if (m) ok = (branch === '' || m[1] === branch) && (m[2] === 'remote' ? value === 'origin' : value === 'refs/heads/' + m[1]);
    if (!ok || seen.has(key)) bad.push(key);
    seen.add(key);
  }
  if (bad.length) throw new Stop(1, 'The fresh clone has git settings that a fresh clone does not have (' + [...new Set(bad)].slice(0, 5).join(', ') + '). Nothing was pushed.');
  const hooks = join(dir, '.git', 'hooks');
  const live = existsSync(hooks) ? readdirSync(hooks).filter((n) => !n.endsWith('.sample')) : [];
  if (live.length) throw new Stop(1, 'The fresh clone has hooks (' + live.slice(0, 5).join(', ') + '). Nothing was pushed.');
  if (existsSync(join(dir, '.git', 'info', 'attributes'))) throw new Stop(1, 'The fresh clone has .git/info/attributes. Nothing was pushed.');
}

/** The environment of the two git commands that carry the token (clone, push): the caller's without secrets, plus ONE header, scoped to github.com. */
export function authEnv(env, token) {
  const out = gitBaseEnv(env);
  out.GIT_CONFIG_COUNT = '1';
  out.GIT_CONFIG_KEY_0 = 'http.https://github.com/.extraheader';
  out.GIT_CONFIG_VALUE_0 = 'AUTHORIZATION: basic ' + Buffer.from('x-access-token:' + token).toString('base64');
  return out;
}

/** The push: to the URL itself, never to the remote name `origin` (a late remote.origin.pushurl or receivepack would not be read). */
export function pushArgs(remote, branch) {
  return ['push', '--quiet', remote, 'HEAD:refs/heads/' + branch];
}

/** The git subcommand of an argument list (`-c k=v` pairs and other leading options skipped): what an error should name. */
export function gitSubcommand(args) {
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '-c' || args[i] === '-C') { i++; continue; }
    if (!String(args[i]).startsWith('-')) return args[i];
  }
  return String(args[0] ?? 'command');
}

/**
 * Deletes a folder that holds nothing secret and is not needed any more: retries, then warns and carries on. A Windows lock (a virus
 * scanner on a fresh pack file) must not stop a promotion; the folder is under a temp folder that is removed at the end anyway.
 * `rm` is a test hook. Returns true when the folder is gone.
 */
export function removeBestEffort(path, rm = rmSync) {
  for (let i = 0; i < 3; i++) {
    try {
      rm(path, { recursive: true, force: true, maxRetries: 3 });
      return true;
    } catch (e) {
      if (i === 2) console.warn('promote-dist: could not delete ' + path + ' (' + String(e && e.message).split('\n')[0] + '); carrying on, it holds nothing secret.');
    }
  }
  return false;
}

// git with line-ending conversion off, so the commit holds exactly the bytes the build wrote (the Windows default
// core.autocrlf=true would otherwise rewrite the check-out and the commit).
function git(dir, args, opts = {}) {
  // Hooks and the file system monitor are switched off for every git command (a hook or core.fsmonitor is code that would run with
  // the environment of the command, which carries the token for clone and push).
  const r = run('git', ['-c', 'core.hooksPath=' + devNull, '-c', 'core.fsmonitor=false', '-c', 'core.autocrlf=false', '-c', 'core.safecrlf=false', '-c', 'core.eol=lf', ...args], { ...opts, env: opts.env ?? gitBaseEnv(process.env), cwd: dir });
  if (r.status !== 0 && !opts.allowFail) {
    const why = String(r.stderr || '').trim().split('\n').slice(-3).join(' | ') || 'exit ' + r.status;
    throw new Stop(1, 'git ' + gitSubcommand(args) + ' failed: ' + why);
  }
  return r;
}

function shellStep(label, command, cwd, env) {
  console.log('> ' + label + ': ' + command);
  // A shell line, because the test hooks are whole command lines and `npm` is npm.cmd on Windows.
  const r = run(command, [], { shell: true, cwd, env, show: true });
  if (r.status !== 0) throw new Stop(1, label + ' failed (exit ' + r.status + '). Nothing was pushed.');
}

// check-dist exits 0 equal, 1 different, 2 cannot run. Its own text goes to the terminal.
function checkDist(repoDir) {
  const r = run(process.execPath, [join(repoDir, 'web', 'scripts', 'check-dist.cjs'), '--repo', repoDir], { cwd: repoDir, env: gitBaseEnv(process.env) });
  const text = (String(r.stdout || '') + String(r.stderr || '')).trim();
  if (text) console.log(text);
  if (r.status !== 0 && r.status !== 1) throw new Stop(1, 'check-dist could not run (exit ' + r.status + ').');
  return r.status === 0;
}

// Settings of the person running this that would make a fresh clone carry config the allow list refuses (git 2.45+ writes
// extensions.refstorage for init.defaultRefFormat=reftable or feature.experimental; 2.54+ writes extensions.submodulepathconfig;
// core.sharedRepository writes core.sharedrepository and receive.denynonfastforwards). Pinned for the commit clone only.
export const CLONE_PINS = ['-c', 'init.defaultRefFormat=files', '-c', 'init.defaultSubmodulePathConfig=false', '-c', 'core.sharedRepository=false'];

/**
 * The fresh clone of `branch` from `remote` in tmp/repo, checked with assertFreshClone. `env` carries the token for a GitHub remote.
 * `afterClone(dir)` is a test hook: it runs between the clone and the check (a stand-in for something that plants in the clone).
 */
export function makeCommitClone({ tmp, remote, branch, env, afterClone }) {
  const dir = join(tmp, 'repo');
  // --template= : no hooks or info files from the person's init.templateDir (they would make the freshness check refuse);
  // --origin origin : a clone.defaultRemoteName of the person's would name the remote otherwise.
  git(tmp, [...CLONE_PINS, 'clone', '--quiet', '--template=', '--origin', 'origin', '--single-branch', '--branch', branch, '--depth', '1', remote, dir], { env });
  if (afterClone) afterClone(dir);
  assertFreshClone(dir, { remote, branch });
  return dir;
}

/**
 * A dry run of the commit clone, before the long build: a clone made the same way (from the build clone, on this disk, no token) with
 * this PC's real git settings must pass the same check. A personal git setting the check refuses then costs seconds, not a whole
 * build. The dry clone is deleted best-effort afterwards.
 */
export function dryRunCommitClone({ buildDir, branch, dryTmp, rm }) {
  mkdirSync(dryTmp, { recursive: true });
  try {
    makeCommitClone({ tmp: dryTmp, remote: pathToFileURL(buildDir).href, branch, env: gitBaseEnv(process.env) });
  } finally {
    removeBestEffort(dryTmp, rm);
  }
}

/**
 * The push: the clone is checked again first (see assertFreshClone), then pushed to the URL itself. `beforePush(dir)` is a test hook
 * that runs after the check and before the push (a stand-in for a change in that last moment: the push must not read it).
 */
export function pushBranch(dir, { remote, branch, env, beforePush }) {
  assertFreshClone(dir, { remote, branch });
  if (beforePush) beforePush(dir);
  git(dir, pushArgs(remote, branch), { env });
}

export function main(argv, env = process.env) {
  const o = parseArgs(argv);
  const have = process.version.replace(/^v/, '');
  if (have !== o.node) {
    throw new Stop(2, 'This Node is ' + have + ' but the web files are built with ' + o.node + ' (the version CI uses). Install ' + o.node + ' (for example with nvm or fnm) and run again.');
  }
  for (const tool of ['git']) {
    const r = run(tool, ['--version']);
    if (r.status !== 0) throw new Stop(2, tool + ' is not available.');
  }

  // Environment of the two git commands that carry the token (clone and push): the token (if the remote is on GitHub) goes in as one
  // header for those processes only. Every other git command gets gitBaseEnv, which has no secret in it.
  let gitEnv = gitBaseEnv(env);
  const remote = o.remote || 'https://github.com/' + o.repo + '.git';
  if (remote.startsWith('https://github.com/')) {
    const tok = String(env.GH_TOKEN || env.GITHUB_TOKEN || run('gh', ['auth', 'token']).stdout || '').trim();
    if (!tok) throw new Stop(2, 'No GitHub token: sign in with `gh auth login` (or set GH_TOKEN).');
    gitEnv = authEnv(env, tok);
  }

  // Two folders, in this order: the build one (dependency code writes in it and next to it) and, only after the first is deleted,
  // the commit one. The build itself never runs while the commit clone exists (a process it left running can still touch that clone:
  // see assertFreshClone and the second check before the push).
  const buildTmp = mkdtempSync(join(tmpdir(), 'unravis-dist-build-'));
  let tmp = '';
  try {
    // The install and the build run dependency code: they get a scrubbed environment (buildEnv), not this one and not gitEnv.
    const stepEnv = buildEnv(env, join(buildTmp, 'env'));
    const buildDir = join(buildTmp, 'build');
    const out1 = join(buildTmp, 'out1'), out2 = join(buildTmp, 'out2');
    console.log('> cloning ' + o.branch + ' to build in ' + buildDir);
    git(buildTmp, ['clone', '--quiet', '--single-branch', '--branch', o.branch, '--depth', '1', remote, buildDir], { env: gitEnv });
    if (!existsSync(join(buildDir, 'web', 'scripts', 'check-dist.cjs'))) throw new Stop(1, o.branch + ' has no web/scripts/check-dist.cjs.');
    const web = join(buildDir, 'web');

    // A dry run of the commit clone, before the long build: a clone made the same way (from the build clone, on this disk) with this
    // PC's real git settings must pass the same check. A personal git setting the check refuses then costs seconds, not a whole build.
    console.log('> checking that a commit clone can be made with this PC\'s git settings (before the build)');
    dryRunCommitClone({ buildDir, branch: o.branch, dryTmp: join(buildTmp, 'dry') });

    shellStep('install', o.install, web, stepEnv);
    shellStep('build', o.build, web, stepEnv);
    copyRoots(buildDir, out1);

    // Same build again from a clean install: the bytes must not move. Catches a build that is not reproducible before anything is pushed.
    rmSync(join(web, 'node_modules'), { recursive: true, force: true, maxRetries: 3 });
    shellStep('install again (clean node_modules)', o.install, web, stepEnv);
    shellStep('build again (reproducibility)', o.build, web, stepEnv);
    copyRoots(buildDir, out2);
    if (!sameRoots(out1, out2)) throw new Stop(1, 'The second build gave different bytes than the first: the build is not reproducible here. Nothing was pushed.');

    // The build clone, its environment folders and the second copy are gone before the commit clone exists.
    rmSync(buildDir, { recursive: true, force: true, maxRetries: 3 });
    rmSync(join(buildTmp, 'env'), { recursive: true, force: true, maxRetries: 3 });
    rmSync(out2, { recursive: true, force: true, maxRetries: 3 });
    if (existsSync(buildDir)) throw new Stop(1, 'The build clone could not be deleted (' + buildDir + '). Nothing was pushed.');

    tmp = mkdtempSync(join(tmpdir(), 'unravis-dist-'));
    console.log('> cloning ' + o.branch + ' to commit in ' + join(tmp, 'repo'));
    const dir = makeCommitClone({ tmp, remote, branch: o.branch, env: gitEnv });
    copyRoots(out1, dir);

    let made = 'none';
    if (checkDist(dir)) {
      console.log('The committed web files already equal a fresh build: nothing to commit.');
    } else {
      git(dir, ['add', '-A', '--', ...ROOTS]);
      const staged = git(dir, ['diff', '--cached', '--name-only', '-z']).stdout.split('\0').filter(Boolean);
      const outside = staged.filter((f) => !ROOTS.some((r) => f.startsWith(r + '/')));
      if (!staged.length) throw new Stop(1, 'check-dist reports a difference but git sees none (line endings? see .gitattributes). Nothing was pushed.');
      if (outside.length) throw new Stop(1, 'Unexpected files staged: ' + outside.slice(0, 5).join(', '));
      // The person's own identity when set; a fixed one for a missing name or email. No signing prompt (stdin is closed here).
      const has = (k) => git(dir, ['config', k], { allowFail: true }).stdout.trim() !== '';
      const who = [];
      if (!has('user.name')) who.push('-c', 'user.name=Unravis promotion');
      if (!has('user.email')) who.push('-c', 'user.email=noreply@users.noreply.github.com');
      git(dir, ['-c', 'commit.gpgsign=false', ...who, 'commit', '--quiet', '-m', COMMIT_SUBJECT + ' ' + o.branch.replace(/^promote\//, ''), '-m', 'Generated by tools/release/promote-dist.mjs (Node ' + o.node + '). Only ' + ROOTS.join(', ') + ' change.']);
      made = git(dir, ['rev-parse', 'HEAD']).stdout.trim();
      console.log('Committed ' + staged.length + ' changed file(s) as ' + made.slice(0, 7) + '.');
      // What was committed is what the two builds made (check-dist of the commit against the folders on disk).
      if (!checkDist(dir)) throw new Stop(1, 'The commit does not equal the built files (check-dist). Nothing was pushed.');
    }

    if (made !== 'none') {
      if (o.push) {
        // Checked again right before the one command that carries the token: something that outlived the build (a detached child process
        // can wait for the commit clone to appear) may have planted a setting since the first check. The push goes to the URL, not to
        // `origin`, and the allow list refuses any other remote.* or url.* setting. What is left: a change between this check and the
        // push, and anything that can read this process's environment anyway (same user).
        pushBranch(dir, { remote, branch: o.branch, env: gitEnv });
        console.log('Pushed to ' + o.branch + '.');
      } else console.log('--no-push: the commit stays in ' + dir + ' (--keep).');
    }
    console.log('DIST_COMMIT=' + made);
    return 0;
  } finally {
    try {
      rmSync(buildTmp, { recursive: true, force: true, maxRetries: 3 });
    } catch {
      // a locked file on Windows: the folder is in the temp area, leave it
    }
    if (tmp && !o.keep && o.push) {
      try {
        rmSync(tmp, { recursive: true, force: true, maxRetries: 3 });
      } catch {
        // a locked file on Windows: the folder is in the temp area, leave it
      }
    } else if (tmp) console.log('Kept ' + tmp);
  }
}

// Real paths on both sides: Node gives argv[1] as typed (a symlink, a junction, a subst drive, an 8.3 name) but import.meta.url as the real path,
// and a plain compare would silently skip main() and exit 0 having done nothing.
const isMain = (() => {
  try {
    return !!process.argv[1] && realpathSync(process.argv[1]) === realpathSync(fileURLToPath(import.meta.url));
  } catch {
    return false;
  }
})();
if (isMain) {
  try {
    process.exitCode = main(process.argv.slice(2));
  } catch (e) {
    if (!(e instanceof Stop)) throw e;
    console.error('promote-dist: ' + e.message);
    process.exitCode = e.code;
  }
}

// Rebuilds the generated web files for a promotion and commits them to the promote branch.
// Called by Promote-Develop.ps1 (docs/pipeline/dist-on-promotion.md). Pull requests into develop do not commit
// web/dist, cmd/unravis-setup/ui and cmd/unravis-lite/ui; the promotion does, once.
//
//   node tools/release/promote-dist.mjs --repo owner/name --branch promote/1a2b3c4 [--node-version 22.23.5] [--no-push] [--keep]
//
// What it does, in order:
//   1. Stops unless this Node is exactly the pinned version (CI builds with the same one).
//   2. Clones the branch into a temporary folder (the working copy of the person running it is never touched).
//   3. npm ci and npm run build in web.
//   4. node web/scripts/check-dist.cjs. Same bytes as committed: nothing to do. Different: commits the three folders
//      with the subject "Rebuild web files for promotion <branch>" (ReleaseLib.ps1 knows this subject).
//   5. Deletes web/node_modules, installs again (npm ci), builds once more and runs check-dist again. A build that does not give the same
//      bytes twice stops here (nothing pushed). This proves the build is stable on this PC from a clean install; the Windows runner's
//      check-dist on the promotion pull request is the proof across machines.
//   6. Pushes the branch (a plain push, never a force).
// Last line printed: DIST_COMMIT=<sha> or DIST_COMMIT=none. Promote-Develop.ps1 requires that line and stops without it. Exit 0 ok, 1 build or check failed, 2 cannot run.
//
// Test hooks (used by tools/ci/test-promote-dist.mjs only): --remote <url or path> replaces https://github.com/<repo>.git,
// --install-cmd and --build-cmd replace `npm ci` and `npm run build`.

import { spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { delimiter, join } from 'node:path';
import { fileURLToPath } from 'node:url';

// Keep equal to the node-version in .github/workflows (ci.yml web job, release.yml, fast-gate.yml). tools/ci/test-promote-dist.mjs checks it.
export const NODE_VERSION = '22.23.5';
export const ROOTS = ['web/dist', 'cmd/unravis-setup/ui', 'cmd/unravis-lite/ui'];
export const COMMIT_SUBJECT = 'Rebuild web files for promotion';

// Environment names the install and the build keep. Everything else is dropped (an allow list, not a deny list: a token variable
// nobody thought of is not passed on). Windows names are matched case-insensitively.
const KEEP = /^(PATH|PATHEXT|SYSTEMROOT|WINDIR|COMSPEC|SYSTEMDRIVE|OS|TEMP|TMP|TMPDIR|LANG|LANGUAGE|LC_[A-Z]+|TZ|TERM|NO_COLOR|FORCE_COLOR|PROGRAMFILES|PROGRAMFILES\(X86\)|PROGRAMW6432|COMMONPROGRAMFILES|PROGRAMDATA|ALLUSERSPROFILE|PROCESSOR_ARCHITECTURE|NUMBER_OF_PROCESSORS|HTTP_PROXY|HTTPS_PROXY|NO_PROXY|ALL_PROXY|SSL_CERT_FILE|SSL_CERT_DIR|NODE_EXTRA_CA_CERTS)$/i;
// A kept variable whose value looks like a GitHub token is dropped too (a token pasted into a proxy or certificate variable).
const TOKEN_VALUE = /(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})/;

/**
 * The environment `npm ci` and `npm run build` run in (they run dependency code, so they get nothing that could reach GitHub):
 *  - only the variables in KEEP, no GH_TOKEN, GITHUB_TOKEN, GH_ENTERPRISE_TOKEN, npm_config_* (registry tokens), NODE_AUTH_TOKEN, ...;
 *  - a private, empty HOME (and APPDATA, XDG_*, npm cache and npm rc files) under `scratch`, so no ~/.gitconfig, ~/.npmrc, gh login
 *    (GH_CONFIG_DIR is that empty folder, so `gh auth token` finds nothing) or credential store of the person running this is read;
 *  - git: no system config, an empty global config, no credential helper (GIT_CONFIG_COUNT/KEY/VALUE reset the helper list), no prompt;
 *  - a `gh` stub first on PATH that only fails, so a dependency script that runs `gh auth token` gets no token.
 * Not covered, and said plainly: dependency code that calls a program by its absolute path, or asks the operating system's own
 * credential store directly (Windows Credential Manager, macOS keychain), is not stopped by this. What protects the result is that
 * the lock file pins the dependencies and check-dist compares the bytes; the token git needs exists only in the git environment (gitEnv).
 * Creates folders and files under `scratch`; returns the environment object.
 */
export function buildEnv(env, scratch) {
  const out = {};
  for (const [k, v] of Object.entries(env)) {
    if (typeof v === 'string' && KEEP.test(k) && !TOKEN_VALUE.test(v)) out[k] = v;
  }
  const home = join(scratch, 'home'), cfg = join(scratch, 'gh-config'), bin = join(scratch, 'bin'), cache = join(scratch, 'cache');
  for (const d of [home, cfg, bin, cache, join(home, 'AppData', 'Roaming'), join(home, 'AppData', 'Local')]) mkdirSync(d, { recursive: true });
  const emptyFile = join(scratch, 'empty.cfg');
  writeFileSync(emptyFile, '');
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
    npm_config_cache: cache, npm_config_userconfig: emptyFile, npm_config_globalconfig: emptyFile, npm_config_update_notifier: 'false',
    GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: emptyFile, GIT_TERMINAL_PROMPT: '0', GCM_INTERACTIVE: 'never',
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

// git with line-ending conversion off, so the commit holds exactly the bytes the build wrote (the Windows default
// core.autocrlf=true would otherwise rewrite the check-out and the commit).
function git(dir, args, opts = {}) {
  const r = run('git', ['-c', 'core.autocrlf=false', '-c', 'core.safecrlf=false', '-c', 'core.eol=lf', ...args], { ...opts, cwd: dir });
  if (r.status !== 0 && !opts.allowFail) {
    const why = String(r.stderr || '').trim().split('\n').slice(-3).join(' | ') || 'exit ' + r.status;
    throw new Stop(1, 'git ' + args[0] + ' failed: ' + why);
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
  const r = run(process.execPath, [join(repoDir, 'web', 'scripts', 'check-dist.cjs'), '--repo', repoDir], { cwd: repoDir });
  const text = (String(r.stdout || '') + String(r.stderr || '')).trim();
  if (text) console.log(text);
  if (r.status !== 0 && r.status !== 1) throw new Stop(1, 'check-dist could not run (exit ' + r.status + ').');
  return r.status === 0;
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

  // Private environment for git: the token (if the remote is on GitHub) goes in as one header for this process only.
  const gitEnv = { ...env, GIT_TERMINAL_PROMPT: '0' };
  const remote = o.remote || 'https://github.com/' + o.repo + '.git';
  if (remote.startsWith('https://github.com/')) {
    const tok = String(env.GH_TOKEN || env.GITHUB_TOKEN || run('gh', ['auth', 'token']).stdout || '').trim();
    if (!tok) throw new Stop(2, 'No GitHub token: sign in with `gh auth login` (or set GH_TOKEN).');
    const header = 'AUTHORIZATION: basic ' + Buffer.from('x-access-token:' + tok).toString('base64');
    gitEnv.GIT_CONFIG_COUNT = '1';
    gitEnv.GIT_CONFIG_KEY_0 = 'http.https://github.com/.extraheader';
    gitEnv.GIT_CONFIG_VALUE_0 = header;
  }

  const tmp = mkdtempSync(join(tmpdir(), 'unravis-dist-'));
  const dir = join(tmp, 'repo');
  try {
    // The install and the build run dependency code: they get a scrubbed environment (buildEnv), not this one and not gitEnv.
    const stepEnv = buildEnv(env, join(tmp, 'build-env'));
    console.log('> cloning ' + o.branch + ' into ' + dir);
    git(tmp, ['clone', '--quiet', '--single-branch', '--branch', o.branch, '--depth', '1', remote, dir], { env: gitEnv });
    if (!existsSync(join(dir, 'web', 'scripts', 'check-dist.cjs'))) throw new Stop(1, o.branch + ' has no web/scripts/check-dist.cjs.');
    const web = join(dir, 'web');

    shellStep('install', o.install, web, stepEnv);
    shellStep('build', o.build, web, stepEnv);

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
    }

    // Same build again on the same files: the bytes must not move. Catches a build that is not reproducible before it is pushed.
    rmSync(join(web, 'node_modules'), { recursive: true, force: true, maxRetries: 3 });
    shellStep('install again (clean node_modules)', o.install, web, stepEnv);
    shellStep('build again (reproducibility)', o.build, web, stepEnv);
    if (!checkDist(dir)) throw new Stop(1, 'The second build gave different bytes than the commit: the build is not reproducible here. Nothing was pushed.');

    if (made !== 'none') {
      if (o.push) {
        git(dir, ['push', '--quiet', 'origin', 'HEAD:refs/heads/' + o.branch], { env: gitEnv });
        console.log('Pushed to ' + o.branch + '.');
      } else console.log('--no-push: the commit stays in ' + dir + ' (--keep).');
    }
    console.log('DIST_COMMIT=' + made);
    return 0;
  } finally {
    if (!o.keep && o.push) {
      try {
        rmSync(tmp, { recursive: true, force: true, maxRetries: 3 });
      } catch {
        // a locked file on Windows: the folder is in the temp area, leave it
      }
    } else console.log('Kept ' + tmp);
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

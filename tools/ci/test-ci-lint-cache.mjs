// Self-test for how .github/workflows/ci.yml keeps the built golangci-lint between runs (RV-CI-2).
// Run: node tools/ci/test-ci-lint-cache.mjs
//
// What it holds in place: the tool is built from source with the module's Go (a released binary refuses a module on a newer Go), so
// the cache key must name BOTH the tool version and the Go version, the install must run only when the cache had no copy, the
// copy must be saved right after the build, and the restore and the save must use one key and one folder. A key without the Go
// version would hand a binary built by an old Go to a job that has a new one; an install that always runs makes the cache pointless.
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))

// The steps (as text blocks) of one job.
export function stepsOf(text, job) {
  const lines = text.split(/\r?\n/)
  const start = lines.findIndex((l) => l === `  ${job}:`)
  if (start < 0) return []
  let end = lines.length
  for (let i = start + 1; i < lines.length; i++) if (/^ {2}[A-Za-z0-9_-]+:\s*$/.test(lines[i])) { end = i; break }
  const steps = []
  let cur = null
  for (const l of lines.slice(start + 1, end)) {
    const m = l.match(/^ {6}- name:\s*(.*)$/)
    if (m) { cur = { name: m[1], text: '' }; steps.push(cur) }
    if (cur) cur.text += l + '\n'
  }
  return steps
}

// Problems with the lint-tool steps of a job, as sentences.
export function problems(text, job = 'linux-gates') {
  const bad = []
  const steps = stepsOf(text, job)
  const find = (re) => steps.find((s) => re.test(s.name))
  const where = find(/lint tool, which version/)
  const restore = find(/lint tool, restore/)
  const install = find(/install golangci-lint/)
  const save = find(/lint tool, save/)
  if (!where || !restore || !install || !save) return [`${job}: a step is missing (found: ${steps.map((s) => s.name).join(' | ')})`]
  const order = [where, restore, install, save].map((s) => steps.indexOf(s))
  if (order.some((v, i) => i > 0 && v <= order[i - 1])) bad.push('the steps are not in the order where, restore, install, save')
  if (steps.indexOf(install) > steps.findIndex((s) => /^Go - lint \(/.test(s.name))) bad.push('the lint step runs before the install')
  if (!/\$\(go env GOVERSION\)/.test(where.text)) bad.push('the key does not hold the Go version (go env GOVERSION)')
  if (!/key=golangci-lint-\$\{version\}-/.test(where.text)) bad.push('the key does not hold the tool version')
  if (!/version=v\d+\.\d+\.\d+/.test(where.text)) bad.push('the tool version is not written once as vX.Y.Z')
  if (!/GITHUB_PATH/.test(where.text)) bad.push('the cached folder is not put on PATH (it must be, on a hit and on a miss)')
  const miss = /if:\s*\$\{\{\s*steps\.golangci-cache\.outputs\.cache-hit != 'true'\s*\}\}/
  if (!miss.test(install.text)) bad.push('the install does not run only on a cache miss')
  if (!miss.test(save.text)) bad.push('the save does not run only on a cache miss')
  if (!/GOBIN:\s*\$\{\{\s*steps\.golangci\.outputs\.bin\s*\}\}/.test(install.text)) bad.push('the install does not build into the cached folder (GOBIN)')
  if (!/@\$\{GOLANGCI_LINT_VERSION\}/.test(install.text)) bad.push('the install does not read the version the key was made from')
  for (const [what, s] of [['restore', restore], ['save', save]]) {
    if (!/path:\s*\$\{\{\s*steps\.golangci\.outputs\.bin\s*\}\}/.test(s.text)) bad.push(`${what} does not use the cached folder`)
    if (!/key:\s*\$\{\{\s*steps\.golangci\.outputs\.key\s*\}\}/.test(s.text)) bad.push(`${what} does not use the key from the where step`)
    if (!/uses:\s*actions\/cache\/(restore|save)@[0-9a-f]{40}\b/.test(s.text)) bad.push(`${what} is not pinned to a commit`)
  }
  return bad
}

const ci = fs.readFileSync(path.join(here, '..', '..', '.github', 'workflows', 'ci.yml'), 'utf8')
assert.deepEqual(problems(ci), [], 'ci.yml lint tool cache')

// The checker catches what it is for.
const bad = (edit) => problems(edit(ci)).join('\n')
assert.match(bad((t) => t.replace("RUNNER_OS}-$(go env GOVERSION)",  '')), /Go version/)
assert.match(bad((t) => t.replace(/ {8}if: \$\{\{ steps\.golangci-cache\.outputs\.cache-hit != 'true' \}\}\r?\n( {8}# Built here)/, '$1')), /install does not run only on a cache miss/)
assert.match(bad((t) => t.replace('GOBIN: ${{ steps.golangci.outputs.bin }}', 'X: 1')), /GOBIN/)
assert.match(bad((t) => t.replace('key=golangci-lint-${version}-', 'key=golangci-lint-')), /tool version/)
assert.match(bad((t) => t.replace(/echo "\$\{bin\}" >> "\$GITHUB_PATH"/, 'true')), /PATH/)
assert.match(bad((t) => t.replace('Go - lint tool, save', 'Go - something else')), /a step is missing/)
console.log('ci lint cache: ok')

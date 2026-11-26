// Self-test for how .github/workflows/ci.yml skips jobs for a notes-only pull request (RV-CI-1, BL-461).
// Run: node tools/ci/test-ci-gating.mjs
//
// The rule it holds in place: every job except `changes` (which decides) and `build` (the required gate, which reports a result on every
// run) needs `changes` and runs only when `needs.changes.outputs.code == 'true'`. A job added later without that line would run on a
// docs-only pull request and cost Windows minutes for nothing. And the gate `build` must keep running when jobs are skipped
// (`if: always()`) and must not be able to wait for a job that never starts. The file is read line by line (no YAML library in this repo's tools).
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))

// Returns { jobName: { needs: [names], ifExpr: string|null } } for the top-level jobs of a workflow text.
export function readJobs(text) {
  const lines = text.split(/\r?\n/)
  const jobs = {}
  let inJobs = false
  let cur = null
  for (const line of lines) {
    if (/^jobs:\s*$/.test(line)) { inJobs = true; continue }
    if (!inJobs) continue
    if (/^\S/.test(line) && !line.startsWith('#')) break // the next top-level key
    const job = line.match(/^ {2}([A-Za-z0-9_-]+):\s*$/)
    if (job) { cur = (jobs[job[1]] = { needs: [], ifExpr: null }); continue }
    if (!cur) continue
    const needs = line.match(/^ {4}needs:\s*(.*?)\s*$/)
    if (needs) {
      const v = needs[1].replace(/^\[|\]$/g, '')
      cur.needs = v.split(',').map((s) => s.trim()).filter(Boolean)
      continue
    }
    const cond = line.match(/^ {4}if:\s*(.*?)\s*$/)
    if (cond) cur.ifExpr = cond[1]
  }
  return jobs
}

const GATED = '${{ needs.changes.outputs.code == \'true\' }}'

// Names of the jobs that break the rule, with the reason.
export function problems(jobs) {
  const bad = []
  if (!jobs.changes) bad.push('there is no `changes` job')
  if (!jobs.build) bad.push('there is no `build` job (the required check)')
  for (const [name, j] of Object.entries(jobs)) {
    if (name === 'changes' || name === 'build') continue
    if (!j.needs.includes('changes')) bad.push(`${name}: does not need \`changes\``)
    if (j.ifExpr !== GATED) bad.push(`${name}: its \`if\` is not ${GATED} (it is ${j.ifExpr})`)
  }
  if (jobs.build) {
    if (jobs.build.ifExpr !== '${{ always() }}') bad.push('build: must run `if: ${{ always() }}`, or a skipped job leaves the required check without a result')
    for (const n of jobs.build.needs) {
      if (!jobs[n]) bad.push(`build: needs ${n}, which is not a job`)
    }
  }
  return bad
}

// The workflow in the repo.
const real = readJobs(fs.readFileSync(path.join(here, '..', '..', '.github', 'workflows', 'ci.yml'), 'utf8'))
assert.deepEqual(problems(real), [], 'ci.yml gating')
for (const n of ['desktop-sidecar', 'linux-gates', 'powershell-syntax']) {
  assert.ok(real[n], `${n} is still a job of ci.yml`)
  assert.ok(real[n].needs.includes('changes'), `${n} needs changes`)
}

// The checker itself: it must catch what it is for.
const sample = (extra) => `name: CI
jobs:
  changes:
    runs-on: x
  web:
    needs: changes
    if: \${{ needs.changes.outputs.code == 'true' }}
${extra}  build:
    needs: [changes, web]
    if: \${{ always() }}
`
assert.deepEqual(problems(readJobs(sample(''))), [])
assert.match(problems(readJobs(sample('  extra:\n    runs-on: x\n'))).join('\n'), /extra: does not need `changes`/)
assert.match(problems(readJobs(sample('  extra:\n    needs: changes\n    runs-on: x\n'))).join('\n'), /extra: its `if` is not/)
assert.match(problems(readJobs(sample('').replace('always()', 'success()'))).join('\n'), /build: must run/)
assert.match(problems(readJobs(sample('').replace('[changes, web]', '[changes, web, ghost]'))).join('\n'), /ghost, which is not a job/)
// CRLF text reads the same.
assert.deepEqual(readJobs(sample('').replace(/\n/g, '\r\n')), readJobs(sample('')))
console.log('ci gating: ok')

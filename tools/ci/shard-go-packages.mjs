// Splits the module's Go packages into N shards so CI can run `go test` on them in parallel.
//
//   node tools/ci/shard-go-packages.mjs --shard 2 --of 3        prints shard 2's packages, one per line
//   node tools/ci/shard-go-packages.mjs --check --of 3          prints every shard and fails on a gap or overlap
//
// Every shard job computes the split from the same `go list ./...` output and the same weights file,
// so the shards are disjoint and together hold every package. Weights (tools/ci/go-shard-weights.json)
// only balance the load: a stale or missing number makes one shard slower, it never drops a test.
// Test the logic with: node tools/ci/test-shard.mjs
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))

/** Greedy balance: heaviest package first, onto the lightest shard. Ties break on shard index, then name. */
export function split(packages, weights, defaultWeight, shards) {
  if (!Number.isInteger(shards) || shards < 1) throw new Error(`shard count must be a positive integer, got ${shards}`)
  const unique = [...new Set(packages)]
  const w = (p) => (Object.hasOwn(weights, p) ? weights[p] : defaultWeight)
  const ordered = unique.sort((a, b) => w(b) - w(a) || (a < b ? -1 : a > b ? 1 : 0))
  const out = Array.from({ length: shards }, () => ({ load: 0, packages: [] }))
  for (const p of ordered) {
    let best = out[0]
    for (const s of out) if (s.load < best.load) best = s
    best.packages.push(p)
    best.load += w(p)
  }
  for (const s of out) s.packages.sort()
  return out
}

function arg(name, fallback) {
  const i = process.argv.indexOf(`--${name}`)
  return i === -1 ? fallback : process.argv[i + 1]
}

function main() {
  const of = Number(arg('of', '1'))
  const check = process.argv.includes('--check')
  const shard = Number(arg('shard', '0'))
  const weightsFile = arg('weights', join(here, 'go-shard-weights.json'))
  const listFile = arg('packages', '')
  const cfg = JSON.parse(readFileSync(weightsFile, 'utf8'))
  const listing = listFile
    ? readFileSync(listFile, 'utf8')
    : execFileSync('go', ['list', './...'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'] })
  const packages = listing.split(/\r?\n/).map((l) => l.trim()).filter(Boolean)
  if (packages.length === 0) throw new Error('no Go packages found')
  const shards = split(packages, cfg.weights ?? {}, cfg.default ?? 2, of)

  if (check) {
    const seen = new Map()
    shards.forEach((s, i) => s.packages.forEach((p) => seen.set(p, [...(seen.get(p) ?? []), i + 1])))
    const bad = packages.filter((p) => (seen.get(p) ?? []).length !== 1)
    shards.forEach((s, i) => console.log(`shard ${i + 1}: ${s.packages.length} packages, weight ${s.load}`))
    if (bad.length) {
      console.error(`not in exactly one shard: ${bad.join(', ')}`)
      process.exit(1)
    }
    console.log(`ok: ${packages.length} packages, each in exactly one of ${of} shards`)
    return
  }
  if (!Number.isInteger(shard) || shard < 1 || shard > of) throw new Error(`--shard must be 1..${of}`)
  const known = new Set(Object.keys(cfg.weights ?? {}))
  const unknown = packages.filter((p) => !known.has(p))
  console.error(`shard ${shard}/${of}: ${shards[shard - 1].packages.length} packages, weight ${shards[shard - 1].load} (${unknown.length} packages use the default weight)`)
  console.log(shards[shard - 1].packages.join('\n'))
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main()

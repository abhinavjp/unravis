// Self-test for shard-go-packages.mjs. Run: node tools/ci/test-shard.mjs
import assert from 'node:assert/strict'
import { split } from './shard-go-packages.mjs'

const pkgs = Array.from({ length: 40 }, (_, i) => `m/p${String(i).padStart(2, '0')}`)
const weights = { 'm/p00': 130, 'm/p01': 120, 'm/p02': 110, 'm/p03': 80, 'm/p04': 70 }

for (const n of [1, 2, 3, 4, 7, 60]) {
  const shards = split(pkgs, weights, 2, n)
  assert.equal(shards.length, n)
  const all = shards.flatMap((s) => s.packages).sort()
  assert.deepEqual(all, [...pkgs].sort(), `every package exactly once for ${n} shards`)
}

// Same input, same answer, whatever the input order.
const a = split(pkgs, weights, 2, 3)
const b = split([...pkgs].reverse(), weights, 2, 3)
assert.deepEqual(a, b, 'order of the package list does not change the split')

// Balanced: the heaviest shard is within the heaviest single package of the lightest.
const loads = a.map((s) => s.load)
assert.ok(Math.max(...loads) - Math.min(...loads) <= 130, `balanced, loads ${loads}`)

// Duplicates in the list are collapsed; unknown packages use the default weight.
const dup = split(['m/a', 'm/a', 'm/b'], {}, 5, 2)
assert.equal(dup.flatMap((s) => s.packages).length, 2)
assert.equal(dup.reduce((t, s) => t + s.load, 0), 10)

assert.throws(() => split(pkgs, weights, 2, 0))
console.log('shard-go-packages: ok')

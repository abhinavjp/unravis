# BL-350: dedupe of identical in-flight comparisons (design note, 2026-11-25)

Decided by the factory under the standing rule "small choices are decided and recorded". It changes what a host sees when it repeats a comparison, so it is written down before the code.

## Today
`audit_compare` is the one heavy tool. The limiter lets one run per host (`MaxLiveHeavy` 1) and two in all. A host that sends the same comparison again while the first still runs (a retry after its own timeout, two agent threads asking the same thing) is refused with 429 "comparisons running at once" and told not to loop. The refusal is correct for a different comparison. For an identical one it makes the host ask again later and pay the same cost twice.

## Rule
- Only `audit_compare` shares a run. Key: profile id, profile revision and the keyed digest of the compacted arguments (the same fingerprint the ledger keeps). Two hosts never share, and a re-approval (new revision) never shares with an older call.
- The key is looked up before the limiter. The first call leads and runs as today. An identical call that arrives while it runs follows: it takes one of the host's call slots (the 4 in flight stay a hard cap), writes its own `admit` and `end` rows, and waits for the leader's answer.
- A follower is not charged: no heavy slot, no place in the 12 an hour, and its result bytes are not counted again (the leader's are). It still passes authentication, the grant's tool list and scope, and the revoke gate, with its own cancel and its own deadline.
- It gets the same envelope as the leader, including the same `resultId`.
- Only a leader that ended `ok` shares its answer. A leader that was cancelled, timed out, was refused, lost its ledger, or lost its grant gives nothing: each waiting follower tries again (one becomes the new leader; the rest follow it), at most 3 times, then goes the normal way and meets the limiter.
- A follower that is cancelled or runs out of time while waiting ends with the matching error answer and does not stop the leader.

## Review (Opus, 2026-11-25)
- A follower that takes over as leader reads the identifiers grant at the time it runs, not at its admit row, and keeps only the time it has left of the host's deadline (real clock). Test: `TestATakeoverReadsTheIdentifiersGrantAtTheTimeItRuns`.
- The closing row of a follower refused by the limiter carries its duration.

## What changes for a host
- An identical comparison while the first runs no longer gets a refusal. It waits and gets the same answer.
- A different comparison while one runs is refused as before.
- The ledger shows one row pair per call, so the person sees every ask; only the leader's pair cost a run. No schema change (the store is schema 5, one-way).

## Not decided here
Dedupe across two profiles and across a finished run (the handle cache already answers `audit_get_groups` and the timelines without a new run).

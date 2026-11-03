# TSK-20 drop-invented-max-and-onset

risk: low — deletes an invented number and changes one constant.

## Outcome

The bucket P95 axis no longer uses an invented `* 1.6` ceiling, and the onset minimum is 5 samples (as in the engine).

## Write scope

- `prototype/tracing-paper-PROTOTYPE.html`

## Must not change

- Anything else in `auditBuckets` or the chart that reads `max`.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Locate `auditBuckets` (~247-253). Find the line `s.max = Math.max(s.max, v.p95 * 1.6)` (~250) and the field `max` it fills (~249-251). Remove those lines.
2. Grep the file for `.max` uses that read this value (e.g. in the chart's y-axis scale). Replace each with the largest real value already available (`Math.max` over the plotted p95/p50 values of that bucket series), so the axis shows the data, not an invented ceiling. List each place you changed in `progress.md`.
3. Locate `SUS_TH` (~1491) and change `onsetMinSamples:20` to `onsetMinSamples:5`. Check its use (~1605) needs no other edit.

## Proof

Grep `1.6` near `p95` returns no hit; Grep `onsetMinSamples:5` returns 1 hit. Run `node prototype/flow-check.cjs` or load with `preview_start` and check `read_console_messages` is clean. UNMEASURED if neither can run.

## Depends on

None.

# Packet B8: one-line "what this means for users" for the 12 host cards, new file `parts/14-impact-host.js`

Do exactly this. Create ONE new file `buildout/wireframes/parts/14-impact-host.js`. Read ONLY `buildout/wireframes/parts/14-help-host1.js` and `buildout/wireframes/parts/14-help-host2.js` (use their `what`, `normal`, `issue` texts so the numbers you quote are the ones already written there; never invent a number or name). ES5 only.

File shape (first line verbatim, then exactly 12 assignments in the order of the table):
```
var EVIMPACT = (typeof EVIMPACT === 'object' && EVIMPACT) || {};
EVIMPACT['GS-01-host-cpu-ok'] = '...';
```
Each value is ONE plain-English sentence for a beginner, 25 to 140 characters, ending with a full stop, single-quoted, containing none of `<` `>` `"` and no apostrophes (write "does not", not "doesn't"). It says what the card's result means for the people using the system. Tone by verdict: ok = nothing slowed users down; watch = users may start to feel it soon; bad = users likely felt slow or stuck work or errors; none = we cannot say, name what is missing. Every sentence must be different from the others (cpu, disk, memory, network, run queue, tcp, swap each get their own wording).

| id | verdict | what the sentence must convey |
|---|---|---|
| GS-01-host-cpu-ok | ok | the processor had plenty of spare time, so requests were not waiting for it |
| GS-02-host-disk-ok | ok | the disk answered quickly, so reads and writes were not held up |
| GS-03-host-mem-ok | ok | the machine had enough free memory, so nothing was pushed out to disk |
| GS-04-host-cpu-watch | watch | the processor was getting busy, so requests may start to queue if load grows |
| GS-05-host-disk-watch | watch | the disk was slower than usual, so reads and writes may start to lag |
| GS-06-host-net-watch | watch | the network was close to its limit, so transfers may start to slow |
| GS-07-host-cpu-red | bad | the processor was maxed out, so every request on this machine likely ran slowly |
| GS-08-host-mem-red | bad | the machine ran low on memory, so programs likely slowed or were stopped |
| GS-09-host-disk-red | bad | the disk was overloaded, so reads and writes likely stalled |
| GS-10-host-runqueue-gray | none | we cannot say how many tasks waited for the processor because performance counters are not readable |
| GS-11-host-tcp-gray | none | we cannot say whether network connections were failing because performance counters are not readable |
| GS-12-host-swap-gray | none | we cannot say whether memory spilled to disk because performance counters are not readable |

## Check (run from `buildout/wireframes`; must print `PASS`)
```
node --check parts/14-impact-host.js && node ../packets/data/check-impact.js parts/14-impact-host.js host
```

## Output
The full file in one code block headed `### parts/14-impact-host.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.

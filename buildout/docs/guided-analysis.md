# Guided analysis

The guided analysis walks a slowdown through eleven steps, Step 0 to Step 10. It reads data you already have, turns it into plain facts, reads signals off the facts, and then checks a catalog of suspects against the signals. It never names what is to blame. It says what fits the data, what does not, and what could not be tested.

This page describes the method. The screens that show it come with the guided analysis screens in a later phase; nothing here depends on them.

## How the engine thinks

The engine works in three layers, always in this order.

1. **Facts.** Numbers computed from the data with no judgment: the middle and the slow end of the times, the minute the slowdown started, how the time splits, how many calls arrived, and which changes sit near the start.
2. **Signals.** Each signal reads facts and answers yes, no or unknown. A signal says "no" only when the data was read and showed nothing. When the data is missing or too thin, the answer is unknown.
3. **Evaluation.** Each suspect names the signals it needs and the signals that normally go with it. The result is one of four words.

| Word | Meaning |
| --- | --- |
| Consistent | The signals it needs read yes, and none that normally go with it read no. |
| Partly consistent | The main signal reads yes, but one that normally goes with it reads no or unknown. |
| Contradicted | A signal it needs read no on data that was actually read. |
| Cannot test yet | A signal it needs is unknown, as the data is missing. |

Unknown never counts as no. Missing data never contradicts a suspect.

### Readiness words per step

Every step shows one readiness word.

| Word | Meaning |
| --- | --- |
| Ready | The step ran on all the data it needs. |
| Partly ready | The step ran on some of the data. What was not read is listed. |
| Cannot test yet | A source the step needs is missing. The step does not pass. |
| Failed | The step could not run on what it was given: a window with no later end, two windows that overlap, or an audit source that could not be read. |

A step with missing data never shows a pass. In this first version only the audit and the IIS logs are read, so Step 1 stays Partly ready at best, and Steps 7, 8 and 9 say Cannot test yet until their own data is imported.

### Suspect colours

| Colour | Meaning |
| --- | --- |
| Red | The most likely suspect. It is red only when its tier moved clearly first. Data-quality suspects never compete for red. |
| Orange | Likely. It fits the data, or it moved together with others. |
| Green | Ruled out by the data we have. This is not a claim about data we do not have. |
| Grey | Not sure, not testable, or does not apply. Grey is never a pass. |

Each suspect also shows the cheapest next check that would tell it apart from its neighbours. Suspects about the host, MongoDB and SQL Server stay grey until their own data is imported.

### Names that never change

Every number the engine compares against sits in one table of named thresholds. The table has a version, and the version travels with every result, so an answer can always be read against the rules it was made under. Changing a threshold means changing the version; a test fails otherwise. The current version is 2026-10-27.

The values that matter most:

- A change is meaningful only when it is at least 30 in 100 **and** at least 100 milliseconds. Both must hold.
- The slowdown starts at the first minute whose slow end stays above twice the reference for five minutes in a row. A minute needs at least five calls to count.
- A group needs 20 calls before its P95 is shown and 100 before its P99 is shown.
- Clocks on different hosts may differ by up to 1 second, so onsets less than a minute and a second apart are not put in order.
- A gap of 2 minutes or less in the data is a warning and the step runs on the minutes that exist. A longer gap makes those minutes Cannot test yet. Missing minutes are never counted as zero.

## Step 0. Frame

> **In a nutshell:** A slow period only means something next to a normal one. Pick the slow stretch and a normal stretch from before it, and we compare the two.

You choose the slow window, the normal window and, if you like, one route. Everything is read in UTC. A window whose end is not after its start, or two windows that overlap, make the step Failed. If the normal window sits after the slow window, or holds fewer than 20 calls, the step is Partly ready and says why.

## Step 1. Trust data

> **In a nutshell:** Before reading any number we check that the data can be trusted: clocks agree, no call is cut off, and nothing is missing.

The engine counts calls that end before they start, or that last longer than six hours, as clock anomalies. They are left out and counted. It looks for gaps: minutes with no calls where the normal window would have had at least five a minute. It compares the number of audit calls with the number of IIS requests to see whether the audit missed part of the traffic. With only the audit, Step 1 never reads Ready; it says what it could and could not check.

## Step 2. Shape

> **In a nutshell:** We find when things first got slower, whether it was a sudden jump, a slow climb or a repeating pattern, and how many routes moved.

The start is the first minute whose slow end stays above twice the reference for five minutes in a row. The shape is periodic when at least three slow bursts start a whole number of 15 or 60 minutes apart; otherwise it is a step when the slow end was at or below one and a half times the reference in the two minutes before the start, and a ramp when it was already building. The scope is one route, a few routes (up to three), many routes or everything (at least four routes in five).

If the audit shows no clear start but the time outside the app does, the start is taken from the outside time and the step says so.

## Step 3. Time split

> **In a nutshell:** Each call's time is split into audit set-up, the work itself and time outside the app. The part that grew most shows where to look first.

A call's recorded time is audit set-up (A) plus the action (C). Payload capture (P) comes after and is outside it. Time outside the app (O) is the IIS time taken, minus the recorded time, minus P. Each part's growth is shown as a share of the extra time. A part is dominant when it holds at least half of the extra time and has risen by a meaningful amount.

O needs the IIS logs. Without them O is Cannot test yet; it is never shown as zero. The IIS logs are matched to audit calls by their end time within 3 seconds and the same route, one to one. If fewer than half of the audit calls find a match, O is not shown.

## Step 4. Load

> **In a nutshell:** We set the number of calls arriving against how long each one takes, to see whether a pool or a limit could have filled up.

Calls in flight equal the arrival rate times the time each call spends. The audit covers a slice of the traffic, so the engine uses a planning arrival rate of 40 calls a second and shows the measured audit rate next to it. If you enter a pool limit, the engine reports whether the calls in flight reached 95 in 100 of it, and whether the top stayed flat for at least three minutes within 5 in 100 of one level. A flat top is a sign that something filled up; it does not say what.

## Step 5. Changes

> **In a nutshell:** We place the changes you listed next to the start of the slowdown. A change counts only if it came just before the start, not if it merely overlapped.

A listed change leads the start when it sits within 30 minutes before it and not after it. An empty list entered on purpose reads "No change listed". A list you did not enter reads "Cannot test yet". The list is typed by hand, so the step always says so. Your release labels are shown exactly as you typed them.

## Step 6. App tier

> **In a nutshell:** We check the app's own queues and pools, using what the audit and the IIS logs can show. What they cannot show is marked as not tested.

The audit and the IIS logs can show retried calls (the same client calling the same route again within 10 seconds; a rise counts at 30 in 100 more, with at least 5 retried calls and at least 2 in every 100 calls retried), server errors and their order against the start, and whether outside time grew. Thread counts, queue lengths and pool counters are not in this data; the suspects that need them are listed as not tested.

## Step 7. SQL

> **In a nutshell:** We check SQL Server against its own data: waits, blocking and plans. Until that data is imported, this step says Cannot test yet.

This version reads only the audit and the IIS logs, so this step reports Cannot test yet and lists what to import. The seam for the SQL Server data is in place, so the later phase fills it in without changing the other steps.

## Step 8. Mongo

> **In a nutshell:** We check MongoDB against its own data: slow operations, cache and disk. Until that data is imported, this step says Cannot test yet.

Same as Step 7. Even with MongoDB data, this step stays at Partly ready in the first release.

## Step 9. Host

> **In a nutshell:** We check the host against its own counters: CPU, memory, disk and network. Until that data is imported, this step says Cannot test yet.

Same as Step 7. Even with host data, this step stays at Partly ready in the first release.

## Step 10. Origin

> **In a nutshell:** We line up when each tier first moved. The earliest is the origin and the others followed or amplified it. If they moved together, we say there is no clear origin.

Each tier that can be dated gets a start minute: the arrival rate, audit set-up, the action, outside time, and later the SQL, Mongo and host lanes. The engine then reads them in order:

- One tier clearly first (more than a minute and a second ahead of the others): that tier is the origin, the others are victims or amplifiers.
- Tiers within a minute and a second of each other: they moved together, and the engine says there is no clear origin.
- Only one tier could be dated: it is named the origin only if every other tier was read in every slow minute and held flat. If any tier could not be read, the answer is not enough data.
- No tier could be dated: no origin is named.

How sure the engine is shows as one word: None, Low, Medium or High. None means there is no answer. Low means the audit alone, or no clear origin. Medium means the audit and the IIS logs. High means the audit, the IIS logs and at least one other tier's own data. A clock anomaly or a long gap lowers it one step. There is no score and no percentage.

The step also lists the sources used, the sources missing, the suspects that could not be tested, and one check that would overturn the answer.

## What the engine does not do yet

- It does not read SQL Server, MongoDB or host data. Those lanes stay Cannot test yet.
- It does not compare against the same weekday and hour of earlier weeks; the normal window is the one you pick.
- It does not read shared stalls, error surges or near-saturation signals.

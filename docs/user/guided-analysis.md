# Guided analysis

The guided analysis walks a slowdown through eleven steps, Step 0 to Step 10. It reads data you already have, turns it into plain facts, reads signals off the facts, and then checks a catalog of suspects against the signals. It never names what is to blame. It says what fits the data, what does not, and what could not be tested.

This page describes the method first, then the screens that show it (see [The screens](#the-screens)). The screens live in the case: open a case, then choose **Open guided analysis**. Each screen is shown in dark and light.

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

O needs the IIS logs. Without them O is Cannot test yet; it is never shown as zero. The IIS logs are matched to audit calls by their end time within 3 seconds and the same route, one to one. If fewer than half of the audit calls find a match, O is not shown. O is also not shown when the matched pairs do not line up at one steady clock difference between the two hosts, as they may then be different calls on the same route. A steady difference of more than 1 second is shown as a note on Step 1.

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

This step reads one day of a SQL Server with four fixed read-only checks: server start and thread pool, the error log, Query Store, and Agent jobs and backups. A check that cannot run says Cannot test yet and lists what is missing. With no SQL Server read in the case, the whole step says Cannot test yet and lists what to add. See [Sources](sources.md).

## Step 8. Mongo

> **In a nutshell:** We check MongoDB against its own data: slow operations, cache and disk. Until that data is imported, this step says Cannot test yet.

The step reads the slow operations the profiler recorded and when they began. Other Mongo questions are not read yet and are listed as Cannot test yet. Even with MongoDB data, this step stays at Partly ready in the first release.

## Step 9. Host

> **In a nutshell:** We check the host against its own counters: CPU, memory, disk and network. Until that data is imported, this step says Cannot test yet.

This step reads the CPU, memory, disk and network counters of a Blackbox. With no Blackbox in the case it says Cannot test yet. Even with host data, this step stays at Partly ready in the first release.

## Step 10. Origin

> **In a nutshell:** We line up when each tier first moved. The earliest is the origin and the others followed or amplified it. If they moved together, we say there is no clear origin.

Each tier that can be dated gets a start minute: the arrival rate, audit set-up, the action, outside time, and the SQL, Mongo and host lanes when the case holds their data. The engine then reads them in order:

- One tier clearly first (more than a minute and a second ahead of the others): that tier is the origin, the others are victims or amplifiers.
- Tiers within a minute and a second of each other: they moved together, and the engine says there is no clear origin.
- Only one tier could be dated: it is named the origin only if every other tier was read in every slow minute and held flat. If any tier could not be read, the answer is not enough data.
- No tier could be dated: no origin is named.

How sure the engine is shows as one word: None, Low, Medium or High. None means there is no answer. Low means the audit alone, or no clear origin. Medium means the audit and the IIS logs. High means the audit, the IIS logs and at least one other tier's own data. A clock anomaly or a long gap lowers it one step. There is no score and no percentage.

The step also lists the sources used, the sources missing, the suspects that could not be tested, and one check that would overturn the answer.

## The screens

Open a case and choose **Open guided analysis**. The page has a left rail with Steps 0 to 10, a tab row (Steps, Checklist, After the fix, Nearby activity, Export) and one **Run the steps** button. The windows are typed in UTC. A run only reads: it cannot change a case or a source.

Every step page has the same frame: the step name, a status sticker, an **In a nutshell** sentence, the reasons for any step that could not be fully tested, the body, and Previous step and Next step.

Status words are fixed. After a run they are Ready, Partly ready, Cannot test yet or Failed. Before a run a step reads Waiting, during a run Running, and both are hatched, never green. These two and the **Out of date, rerun** line are the only words beyond the four. They say a step has no answer to show yet, or that the answer is old, and never that anything passed. A run that fails before any answer arrives reads Failed on every step. When the windows, sources or lists change after a run, each step after Step 0 says **Out of date, rerun**. The numbers on screen stay those of the earlier run until you run again.

The rail rows are buttons (outlined). The status next to each row is a sticker: information, not a button. There is one primary button on a screen. On the Steps tab it is **Run the steps** until an answer is on screen, then **Run again** (outlined) takes over.

You can jump to any step without running first. It then shows the last run, or says to run the steps first.

Use **Previous step**, **Next step** and the step list to move between steps. The browser's Back button does not step back through them, and Reload does not return to the same step. Steps and cases have no address of their own in this release.

### Step 0 screen

![Step 0, the question, dark](screenshots/P6/p6-step-0-dark.png)
![Step 0, the question, light](screenshots/P6/p6-step-0-light.png)

The question: an incident window and a normal reference window, both in UTC, an optional API to focus on, an optional SqlClient pool limit, and a list of changes (up to 50, each with a time and a label). Quick picks (Last hour, Today, Yesterday) fill the incident window and a normal period of the same length just before it. After a run the page shows the onset Unravis found and how many calls the run used. When no onset is found, the page says that is a finding, not an error, and offers **Carry on without an onset**.

A change needs a time and a name. Changes you type are used in Step 5 only when **I have listed every change I know of** is ticked.

Windows that overlap, run backwards or are longer than 7 days are explained in a sentence and the run button waits.

### Step 1 screen

![Step 1, trust the data, dark](screenshots/P6/p6-step-1-dark.png)
![Step 1, trust the data, light](screenshots/P6/p6-step-1-light.png)

Trust the data: cards for the incident and reference windows (calls, how many had no start time or no end time), one row per source with its status, and the gaps found in the minutes. If a window has too few calls, the page says so and offers to widen the incident window.

### Step 2 screen

![Step 2, shape, dark](screenshots/P6/p6-step-2-dark.png)
![Step 2, shape, light](screenshots/P6/p6-step-2-light.png)

Shape: the minute-by-minute line of the slow end, the onset, the shape of the slowdown in a word and a table of APIs ranked by the biggest rise. A rise counts only when it passes both marks, 30 percent and 100 ms. When nothing moved, the page says what the two marks were.

### Step 3 screen

![Step 3, time split, dark](screenshots/P6/p6-step-3-dark.png)
![Step 3, time split, light](screenshots/P6/p6-step-3-light.png)

Time split: the parts A, C, P and O drawn as bars on one scale. Only parts that could be tested are drawn. Part O needs an IIS file, and when it is missing the page names it as Cannot test yet.

### Step 4 screen

![Step 4, load, dark](screenshots/P6/p6-step-4-dark.png)
![Step 4, load, light](screenshots/P6/p6-step-4-light.png)

Load: four measures for both windows (calls a second, time in system, calls in flight on average, calls in flight at the peak), each with No data when it could not be measured, and the pool answer when a limit was entered.

### Step 5 screen

![Step 5, changes, dark](screenshots/P6/p6-step-5-dark.png)
![Step 5, changes, light](screenshots/P6/p6-step-5-light.png)

Changes: each change you listed with its time in UTC, its label and how it sits against the onset, and the suspects of that group. Changes are only compared when you list them in Step 0.

### Step 6 to Step 9 screens

![Step 6, application, dark](screenshots/P6/p6-step-6-dark.png)
![Step 6, application, light](screenshots/P6/p6-step-6-light.png)
![Step 7, SQL Server, dark](screenshots/P6/p6-step-7-dark.png)
![Step 7, SQL Server, light](screenshots/P6/p6-step-7-light.png)
![Step 8, MongoDB, dark](screenshots/P6/p6-step-8-dark.png)
![Step 8, MongoDB, light](screenshots/P6/p6-step-8-light.png)
![Step 9, host, dark](screenshots/P6/p6-step-9-dark.png)
![Step 9, host, light](screenshots/P6/p6-step-9-light.png)

One page per tier: application, SQL Server, MongoDB and host. Each page shows the findings the engine made for the tier and a card for every suspect of the tier. The SQL, Mongo and Host pages say Cannot test yet until the case holds that source, and offer **Add data**. A suspect that could not be tested is grey and never counts as ruled out.

### Step 10 screen

![Step 10, origin and suspects, dark](screenshots/P6/p6-step-10-dark.png)
![Step 10, origin and suspects, light](screenshots/P6/p6-step-10-light.png)
![Step 10, origin and suspects, dark, narrow](screenshots/P6/p6-step-10-dark-narrow.png)
![Step 10, origin and suspects, light, narrow](screenshots/P6/p6-step-10-light-narrow.png)
![Step 10 when no origin is clear, dark](screenshots/P6/p6-no-clear-origin-dark.png)
![Step 10 when no origin is clear, light](screenshots/P6/p6-no-clear-origin-light.png)

Origin and suspects. The page shows, in order:

1. **What moved first**: the origin and victim table (tier, when it first moved in UTC, what moved, role). When two tiers moved together the page says **No clear origin** and lists them without calling one first.
2. **Colours**: red most likely, orange likely, green ruled out, grey not sure. Grey is never a pass. The green card reads "Ruled out by the data we have", so a ruling-out is never worded as stronger than the data behind it.
3. **Suspects**: each ranked suspect as a card with its colour word, why it fits, the evidence kind (observed on its own data or inferred), the check that would overturn it, and **Add this check** to the checklist. Grey suspects follow, then the ones ruled out (collapsed).
4. **The cheapest check** to make next.
5. **How sure we are**: None, Low, Medium or High, the sources used and missing, and the one check that would overturn the answer.

Unravis does not name a culprit.

### Nearby activity screen

This panel has no screen of its own: it is captured inside the Tools screens.

Pick a moment in UTC and a span of 0 to 120 minutes either side. The audit lane lists the calls of the window (the first 100 at most) as bars, banded by their time: under 1 second, 1 to 2 seconds, over 2 seconds. These bands are not the suspect colours. A call with no time is shown as No time, never as fast. The SQL Server, MongoDB and Host lanes read Cannot test yet. The page never shows a user, an address or a URL. Nearby activity is not included in the report.

### Checklist screen

![The Checklist tab, dark](screenshots/P6/p6-checklist-dark.png)
![The Checklist tab, light](screenshots/P6/p6-checklist-light.png)
![The Checklist tab, dark, narrow](screenshots/P6/p6-checklist-dark-narrow.png)
![The Checklist tab, light, narrow](screenshots/P6/p6-checklist-light-narrow.png)

Four lists: Need to do, Working on, What we learned and Ruled out. A counter shows how many items are open and how many are done. You can add an item (up to 300 characters, 500 items in all), move it to another list, or delete it. **Add suggested checks from Step 10** adds each suggested check once. On a closed case the lists can be read but not changed.

### After the fix screens

![After the fix, before a later day is picked, dark](screenshots/P6/p6-after-fix-1-dark.png)
![After the fix, before a later day is picked, light](screenshots/P6/p6-after-fix-1-light.png)
![After the fix, with a later day compared, dark](screenshots/P6/p6-after-fix-2-dark.png)
![After the fix, with a later day compared, light](screenshots/P6/p6-after-fix-2-light.png)

Pick a later day. Unravis compares the same hours on that day with the incident window you ran, using the incident as the normal period. The page shows the earlier day, the later day and the change for the slow end, the middle, the mean and the load. The verdict is one of Fixed, Better (not fixed), Unchanged, Worse, Partly there, No data for that day or The days are not alike.

- Fixed needs a known normal range and a slow end back inside it.
- A fall that is still above normal is Better, not Fixed.
- A day with missing minutes is Partly there. Missing minutes are not counted as zero.
- If the two days are not alike (the calls differ by more than 50 percent, or the API mix moved by more than 0.3, or the later day has less than 0.8 of the minutes) the page warns and never says Fixed. **Pick a more alike day** and **Compare one route only** are offered.

**Record result** writes one line (300 characters at most) under What we learned. Nothing is recorded without a click.

### Export screens

![The Export tab, dark](screenshots/P6/p6-export-1-dark.png)
![The Export tab, light](screenshots/P6/p6-export-1-light.png)
![The report preview after a run, dark](screenshots/P6/p6-export-2-dark.png)
![The report preview after a run, light](screenshots/P6/p6-export-2-light.png)

The report is made from the run on screen. The server reads the case data again to build it, so a large case can take a moment. Tick what to include: the headline figures, the suspects, the checklist and the sources. **Download report (HTML)** is the one primary button. **Download data (CSV)** has the numbers only. **Preview** shows the report in a sandboxed frame before you save.

- The file never contains a request payload, a user id, a connection detail or a password.
- The CSV starts with a byte-order mark so a spreadsheet reads it as UTF-8, and any cell that starts with =, +, - or @ is written as text so it cannot run as a formula.
- The file is saved only when its checksum matches what was built. If it does not, nothing is saved and the page says so.
- A report made from a run that is out of date, or from a read that stopped at its limit, says so at the top and the button asks you to confirm.
- An empty case still exports, with a note that nothing was analysed.

## What the engine does not do yet

- It does not read SQL Server, MongoDB or host data. Those lanes stay Cannot test yet.
- It does not compare against the same weekday and hour of earlier weeks; the normal window is the one you pick.
- It does not read shared stalls, error surges or near-saturation signals.

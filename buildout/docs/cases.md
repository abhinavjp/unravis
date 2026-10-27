# Cases

A case keeps one investigation together: the files you added, your theories, the health checks you ran, and the story of what you did. Cases are saved on this computer, in your Unravis folder.

## The case list

The start page lists your cases. Open one, type a case code, restore a case from a case file, or create a new case. A closed case is marked Closed.

![The case list, dark](../screenshots/P1/p1-case-list-dark.png)
![The case list, light](../screenshots/P1/p1-case-list-light.png)

The case list pictures show made-up cases from the capture demo. They are not real investigations.

## Create a case

Press Create a new case. The guide walks you through the steps. Step 1 asks what you are looking at.

![Create a case, step 1, dark](../screenshots/P1/p1-create-1-dark.png)
![Create a case, step 1, light](../screenshots/P1/p1-create-1-light.png)

### Pressing Create twice, reloading, or closing the window

Each run of the guide has its own request number, kept in this browser tab. Unravis makes one case for one request number. So these all end with one case, not two:

- You press Create twice, or press it again because nothing seemed to happen.
- You reload the page in the middle of Create. The same title picks up the same request number.
- The computer or Unravis stops in the middle of Create. When Unravis starts again it finishes the steps that were left.

If you press Create for a case that was already made and then archived, you see "This case was already made. Find it on the start screen." Starting the guide again from the start, or changing the title, makes a new case.

## Case home

Case home shows what happened so far, in order, oldest first. It shows only what the case has saved: when it started, the files you added, your theories and your health checks.

![Case home, dark](../screenshots/P1/p1-case-home-dark.png)
![Case home, light](../screenshots/P1/p1-case-home-light.png)

On a narrow window:

![Case home on a narrow window, dark](../screenshots/P1/p1-case-home-dark-narrow.png)
![Case home on a narrow window, light](../screenshots/P1/p1-case-home-light-narrow.png)

## Health checks

Health checks look at the SQL Server or MongoDB your logs depend on and say in plain words what looks healthy and what needs a look. They only read. They run only when you press Run checks.

![Health checks in Case home, dark](../screenshots/P1/p1-health-dark.png)
![Health checks in Case home, light](../screenshots/P1/p1-health-light.png)

The demo has no SQL Server kept and has run no checks, so these pictures show the empty state: "No SQL Server is kept yet" and "No checks have been run". Pictures with results arrive with the health-check phase.

## Export evidence

Export builds a file of the findings in the window you are looking at. You see a preview before anything is saved.

![Export evidence, dark](../screenshots/P1/p1-export-dark.png)
![Export evidence, light](../screenshots/P1/p1-export-light.png)

## Close and Reopen

Close puts a case away without losing anything. A closed case can be read, but nothing can be added to it: no files, no theories, no checks. Close is on the Import page, next to each open case. Case home then says "This case is closed. You can read it, but nothing can be added." and the story shows "Case closed".

Closing is not final. Press Reopen on Case home (or on the Import page, in the row of the closed case) and the case is open again. Reopen:

- brings back the saved MongoDB link and the filters the case had when it was closed;
- never starts anything by itself. Sampling and checks start only when you press them;
- writes "Case reopened" into the story.

A closed case is still erased 30 days after it was closed, unless you reopen it.

Screens: `p2-close` is the case list on the Import page. `p2-closed-readonly` is a closed case with its buttons turned off. `p2-reopen` is the same case with the Reopen button. The pictures are made by `toolsscreensun-screens.ps1 -Update`; until they are saved they are not linked here.

Pressing Reopen twice quickly sends one request. The second press does nothing while the first is running.

## Needs another try

When you create a case and some steps do not go through (an environment list that was not saved, a source that was not tagged, a file that could not be read), the case is still made. What did not go through is kept in the case, in a list called Needs another try. It stays after you close Unravis, and it travels inside a case file.

Each item says what failed, in one of four ways. They are different on purpose:

| Label | What it means | Try again does |
|---|---|---|
| Environments not saved | The environment names were not saved. | Saves them again. |
| Source not saved | A log source was not tagged. | Tags it again. |
| Not added to analysis | A database was connected but not added as an audit source. | Nothing here. The line says to add it again from Connections. |
| Import failed | A file could not be read. | Opens the file picker, because the file has to be chosen again. |

You can also Remove an item from the list. On a closed case the list says "This case is closed. Reopen it to try again." and shows no buttons. The list never keeps passwords or text from your files.

Screen: `p2-retry-list`. The picture is not saved yet.

## Two tabs

You can have the same case open in two browser tabs. Every case action checks what the page believes the case is (open, closed) against what it is now, and only one action runs on a case at a time.

If another tab changed the case first, the page changes nothing and says: "This case changed in another tab. Reload." Press Reload to see the case as it is now. This is the 409 answer with the message id M2. It is different from the two older answers: 423 means the master key is locked, and 409 "untrusted" means the case was not opened because its folder was not here when the key was set up.

Examples: you close a case in tab A, and then add a file in tab B. Tab B is told the case is closed. You press Reopen in two tabs: the first reopens, the second sees M2.

Screen: `p2-changed-elsewhere`. It cannot be captured by the screenshot tool yet, because it needs a second tab to change the case in the middle of the steps. It is checked by hand and by tests.

## Coming later

- Archive, verify, free and restore: Coming in P3.
- Case keys and erase: Coming in P4.

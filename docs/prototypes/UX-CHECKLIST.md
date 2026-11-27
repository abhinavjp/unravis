# UX checklist for the prototype author

Run before the goldfish review (pipeline step 3). Tick each box in the review file. Fix, then tick. If a box cannot be ticked, say why.

Test it by looking at the screen with the explanations hidden: cover the title, forget the context.

## Next step
- [ ] Every screen and every state has one clear next action, visible without scrolling or guessing.
- [ ] In a failed or blocked state the next action is about the failure (see the error, retry, fix), not generic.
- [ ] In a finished, all-green state the screen says it is finished and what comes after (or that nothing is needed).
- [ ] No dead ends: nothing that looks clickable does nothing; nothing important is reachable only by guessing.

## State shown in place
- [ ] The state (passed, failed, running, waiting) is shown where I am looking, with words and an icon shape, not color alone.
- [ ] A change of state shows next to what changed, not only in a banner far away.
- [ ] The failing step and its first error are visible without opening anything else, and the failure's place is one click from the top.
- [ ] "Is everything fine?" can be answered in a few seconds from the top of the screen.

## Plain words
- [ ] Every label and status is a word a new person knows. If a technical term is needed, it is explained in place (a short line under it, not a tooltip only).
- [ ] No internal names, abbreviations, ids or branch names in primary text. If shown, they are secondary and explained.
- [ ] Button text says what will happen ("Retry failed step"), not just a verb ("Run").
- [ ] Numbers carry their unit and meaning ("3 of 8 steps done", not "3/8").

## Everything connects
- [ ] I can tell where I am: which page, which thing it belongs to, how to go back.
- [ ] The story reads in order: what this is, what happened, where the problem is, the steps, what to do. A person can follow it top to bottom.
- [ ] The same thing has the same name everywhere. A name on the list page matches the title on the detail page.
- [ ] Every item in a list leads to the place that explains it.

## Real data and look
- [ ] The prototype uses a real snapshot (project rule: live data), not invented rows.
- [ ] Impeccable and AI Slop Detector were run and findings fixed.
- [ ] Works at phone width; light and dark both readable.
- [ ] Nothing in the prototype says it is a demo in a way that hides what the real screen would show.

# Research: showing coalesced merges in a build queue

Reused from homestretch-run-page research (under 30 days old) plus known patterns; no new web pass.

- GitHub merge queue and GitLab merge trains show a numbered list of queued changes with one status each. Users scan position and state, not logs.
- Buildkite and Jenkins collapse superseded builds into one row with a "replaced by" pointer, so nothing looks lost.
- Pattern chosen: one Queued card with a chip ("5 merges, 1 run"), a collapsed "Covers #..." list, and a failed state that says the run failed without blaming one merge.
- Risk found by goldfish rounds: "covers/merges/run" need a "?" help in the real UI.

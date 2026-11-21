# Backlog pipeline (software factory)

Each backlog item moves through stages. Each stage is a fresh worker. The handoff between stages is one md file in this folder.

1. GROOM  -> `<id>-groom.md`   scope, risks, acceptance, live-path check, slices
2. SPEC   -> `<id>-spec.md`    exact behaviour, files, wire formats, tests
3. PLAN   -> `<id>-plan.md`    ordered small PRs, each with tests and a live check
4. BUILD  -> small PRs         one slice per PR
5. REVIEW -> fresh Opus reviewer per PR, no context from the builder
6. VERIFY -> real data path (owner's PC) or CI; say plainly what is and is not verified
7. MERGE  -> after CI is green; then tick `docs/BACKLOG.md` only when proven on the real data path

Rules: owner rule in `CLAUDE.md` (live data, finish what you start). Items under "Backlog (deferred)" need the owner's go. Footprint goals: minimal CPU, memory, network.

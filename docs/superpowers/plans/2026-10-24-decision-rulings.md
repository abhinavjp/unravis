# Decision rulings R1–R16 (owner-delegated)

- **Date:** 2026-10-24
- **Basis:** the owner asked the implementer to use best judgment on the open decisions and continue. This file records each ruling and its reason.
- **Limit of this document:** it settles design defaults only. It grants no access and speaks for no client, Ops, DBA, Mongo owner or client-security reviewer. Where a decision belongs to one of them, the row says so and the work that needs it stays unbuilt.
- **Reversible:** any row can be overruled by the owner; the cost of each is stated.

| # | Ruling | Why | Cost if wrong |
|---|---|---|---|
| R1 | **Accept** all reversals in §10.1 (2, 6, 11 reversed; 7–10 void; 14, 15 changed). | Each one shrinks an access ask or removes unneeded scope. None adds risk. | Re-open rev 9 §10.1. |
| R2 | **Not mine to rule.** Log preservation and the counter capture need the client's and Ops' authority. Not requested, not assumed. | A change on a client's servers cannot be authorised from here. | T0 evidence is lost with log retention; P6/P3 stay blocked. |
| R3 | **Accept** the §7 matrix as the approval vehicle only. Each source still needs its own owner's approval. | Accepting the vehicle grants nothing. | None. |
| R4–R7 | **Not requested.** Query Store, JEA and the `RequestInitiatedAt` index are not asked for. Exact mode with `maxTimeMS` is measured first. | Each is optional and carries cost for the owner. | A slower exact mode. |
| R8 | **Open.** P14a ships marked "content review pending". Choosing five test users is the owner's. | Only a real non-engineer read can pass the reading-level claim. | Copy may confuse readers. |
| R9 | **Accept** the independent-episode gap of 10 minutes and the `AccountId` coverage threshold of 80 %. | These are the spec's own defaults, tunable per case before looking at data. | Episode counts shift. |
| R10 | **Accept** MCP at the 10 existing tools, at most 3 more later. | No evidence yet that any tool is missing. | None. |
| R11 | **Repo audit done** (T0.10). **Build the safe default now:** an operator flag, off by default, must be set before MCP returns `includeIp` or raw user ids and user agents. The client's written approval of the AI provider path is still required before MCP touches live data. | The model controls `includeIp` today and there is no operator switch. Default-off removes the exposure and grants nothing. | Slightly less detail in MCP output until the flag is set. |
| R12 | **Not mine to rule.** The DBA runs the export dry runs. No SQL import is built. | P3 is held until the dry run exists. | P3 stays unbuilt. |
| R13 | **Not mine to rule.** No live Mongo access is built or used. The fallback is an owner export. | The built-in `read` role is database-wide, so "audit collection only" needs the owner. | Live reads stay off. |
| R14 | **Rule the budgets.** Interactive Mongo queries `maxTimeMS` 10 000. Opt-in census 60 000. Concurrency 1 for shared sources. The `docsExamined` budget is configurable and unset until measured on a restored copy. Broad scans never run live by default. | Conservative, fails closed, and needs no grant. | Slower queries that time out and say so. |
| R15 | **Confirm** "Proven cause" stays out of T1. Any future proof label needs a named reviewer and a linked trace or reproduction. | The ledger can show correlation; it cannot show cause. | None. |
| R16 | **Accept** the default retention of 30 days after case closure as a starting default. Client security must confirm it before the first importer. Per-importer limits are set on each importer's card. | There is no importer yet, so nothing is retained. | Revisit at the first importer. |

## What these rulings let the team build now

- **P1a** (audit safety and coverage), on demo and fixture data only.
- **The MCP privacy switch** from the T0.10 audit.
- **T0.6** code reads, read-only.

## What stays unbuilt

Anything that needs R2, R12, R13, the client's R11 approval, or R8. That covers every importer, P3, live Mongo reads, MCP on live data, and the P14b tour.

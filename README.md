# Unravis

Unravis is a local Windows utility for comparing API request durations with a fixed, preceding reference period. It runs an HTTP service bound only to `127.0.0.1` and embeds the dashboard in the executable.

## Start

Double-click `Start Live.cmd` to open live mode on an available local port, even when a demo is already running. Keep its terminal open while using the dashboard. Startup errors remain visible in that window.

The prebuilt `dist\unravis.exe` runs without Go or Node installed. Double-click it for live connections, or use `run.ps1 -Demo` for samples. To rebuild from source:

```powershell
.\build.ps1
```

Try the deterministic sample data first:

```powershell
.\run.ps1 -Demo
```

For live data, start without `-Demo` and add MongoDB sources in the dashboard. Connections are made when you test or add a source. The browser opens the local dashboard by default; use `-NoBrowser` to suppress that. By default the dashboard picks a free loopback port each time; `-Port` fixes one. At start it prints the link to open, `http://127.0.0.1:<port>/#t=<token>`, and opens that link in the browser. The session token travels only in the part after `#`, which the browser never sends to the server; the page reads it, keeps it for that browser tab, and removes it from the address bar. The token is never written into the dashboard page. If a tab shows "Open Unravis from the link it printed when it started, or restart it.", use the printed link again. The port and token are also written owner-only to `%APPDATA%\APIAuditExplorer\session.json` (or `%AUDIT_CONFIG_DIR%\session.json`) so `--mcp` can find the dashboard; the file is removed when the dashboard shuts down cleanly.

## Live sources and privacy

Enter a server address such as `uk-01-mongo.brainpayroll.co.uk`, `host:port`, or a full `mongodb://` / `mongodb+srv://` URI, without embedded credentials. A bare hostname uses standard MongoDB discovery and the default port 27017; use a full URI for SRV, TLS, or other connection options. Enter the username and password separately, then choose **Test connection & load databases**. Select one or more returned databases and enter the audit collection. Each selected database becomes a separate source, sharing the authenticated server connection while keeping its analysis results distinct. Discovery lists only databases the account is authorized to access and does not save a source. Changing connection fields invalidates the previous discovery result. Manual database entry is available when discovery is restricted.

Connection details remain in the form so you can add more databases from that server. **Clear connection details** clears the draft credentials; it does not remove saved sources. Additions run one at a time and keep successful sources if another database fails; failed selections remain available for retry. The existing eight-source capacity applies across all connections.

The optional authentication database identifies where the MongoDB user was created; it can differ from the selected audit database. Leave it blank to preserve the URI's authentication setting and MongoDB defaults. Password spaces and special characters are preserved. Connection tests and source creation never return the credentials to the browser.

The Sources navigator groups audit databases under their connection. Select databases from one or more connections for the same investigation; the eight-source limit applies across all connections. Connection settings live in the Connections drawer, where you can create a connection or load saved settings. An empty session offers both choices. Saved connections are settings you may reuse; they do not become active sources until you add their databases.

Use the separate calendar and time controls to choose the period to investigate and an earlier comparison period. Inputs use your browser's local time zone; **Show exact UTC times** exposes the stored instants. **More filters and chart settings** contains request filters and chart time-zone settings. Changing chart time zone does not change the queried instants. Investigation settings are remembered in session storage for the current browser tab, including exact date ranges, source selections, filters, chart time zone and response-time target. Refreshing the tab restores these settings without automatically running a query. Closing the tab clears them; another tab starts with its own settings. Reset investigation clears the remembered draft and returns to fresh defaults. Credentials and query results are never included in this browser snapshot. Missing or invalid date/time values block comparison, including local times that do not exist during a daylight-saving clock change. Calculation notes remain available under **How these results are calculated**.

The utility supports MongoDB Server 3.4 and 4.4 or newer, using read-only queries. MongoDB 3.4 is detected through a read-only handshake and handled by a separate legacy driver; modern servers retain the current driver. Use a MongoDB account limited to the audit database and collection, with read permissions only. The utility does not write to MongoDB or create indexes. For query performance, consider an index beginning with `RequestInitiatedAt` on each source collection; confirm index choices against your workload before creating one.

For MongoDB 3.4, charts and request lists stream projected metadata in bounded batches and normalize it locally, because the server lacks newer aggregation operators. A query is limited to 100,000 scanned metadata records and 16 MiB of projected BSON across its selected legacy sources and comparison periods. Individual metadata strings also have size limits. Exceeding a limit fails the query; it does not display partial statistics as complete. Narrow the date range or filter to a controller/action when this happens. Payload bodies are excluded from metadata scans and remain explicit, capped reads. MongoDB 3.4 accounts without database-listing permission can use manual database entry. SSL settings are always respected; legacy support does not automatically disable encryption or certificate checks.

A live source profile contains a label, MongoDB connection settings, database, and collection. Source responses omit connection credentials. Removing a source removes its in-memory profile; it does not delete a recent connection. Do not put credentials in shell history or shared development configuration.

## SSL / TLS and certificate trust

New connections default to **On — verify certificate**. This encrypts traffic and verifies the server certificate and hostname using system trust. For a private CA or self-signed server, choose **On — use certificate file** and import its public PEM certificate (`.pem` or `.crt`, at most 256 KiB). Hostname checks stay enabled. Private keys and combined certificate/private-key files are rejected. Importing here affects this connection, not the Windows trust store.

**From connection URI** preserves the driver's URI TLS settings. **Off** disables transport encryption. **On — skip verification (advanced)** requires explicit selection and disables both certificate-chain and hostname checks; it is intended for temporary diagnosis. TLS options and CA trust are part of the connection identity, so differently trusted connections do not share a client pool. Custom/system/advanced modes use TLS 1.2 or newer.

## Recent connections and saved passwords

**Save connection** remembers the settings without contacting MongoDB, so it works before a successful connection and while a VPN is unavailable. Saving checks local address and certificate settings; it does not confirm connectivity or credentials. Successful connection tests also remember connection settings. **Save password on this Windows account** is optional and off for a new connection. Recent settings and any opted-in passwords are stored together in a Windows current-user DPAPI-encrypted file, normally `%APPDATA%\APIAuditExplorer\connections.dat`. No password is stored in browser local storage or returned by the recent-connections API. Up to 20 records and 1 MiB of encrypted history are retained.

**Load** restores settings and lets the backend use a saved password without sending it to the page. Editing the server, user, authentication database, or trust settings invalidates that saved-password reference. **Forget** deletes the recent record and its saved password; active sources remain connected. **Clear connection details** only clears the current form.

Turning password saving off and saving or testing the connection applies the new preference: it removes the password from disk while allowing the connection details to continue in the current process. After a restart, that recent requires the password again. A failed history save does not prevent using the tested connection. Corrupt/unreadable history is left unchanged and reported; it is never silently overwritten. URI options containing passwords or secret tokens cannot be saved as public recent metadata. Connection failures distinguish settings, network, TLS, sign-in, and database-listing problems using sanitized messages.

Live reads use aggregation and metadata projections. Request and response payload text is read only when explicitly opened for a request. CSV exports contain request metadata and never payload bodies. Driver failures are returned as sanitized categories rather than raw driver error strings.

## Interpreting comparisons

The current interval and reference interval are separate, disjoint windows. The reference interval must end at or before the current interval begins. Each interval can be at most seven days; bucket widths are 10 to 86,400 seconds, with at most 120 time buckets per interval.

The default comparison uses synchronous requests and total duration. Total duration is completion time minus initiation time; API time uses the recorded `ApiTime`. Success and failure outcomes remain separate. Successful HTTP outcomes are 2xx and 3xx; recognized 4xx and 5xx outcomes are failures. Missing or unrecognized values remain unknown or incomplete instead of being counted as successful latency.

P50 (the middle response time) estimates a duration that about half of measured requests finish within. P95 estimates a duration that about 95 out of 100 measured requests finish within; the slowest few may take longer. P95 is not an average or a success percentage. A millisecond (ms) is one thousandth of a second. The reference is the earlier period used for comparison. An API group keeps a source, controller/action, request type and outcome separate, so matching names on different connections never share a history. P50 and P95 are approximate estimates from logarithmic duration buckets. Their displayed intervals describe bucket uncertainty. Comparisons with fewer than 20 valid samples or an undefined near-zero reference are marked accordingly. A historical comparison is not a health check, SLO result, or incident verdict.

## Limits

The utility limits profiles to 8, active operations to 4 overall and 1 per source, request pages to 100 rows, API groups to 10,000, and aggregation output to 40,000 bucket rows and 16 MiB. Summaries that exceed their limits fail instead of presenting partial statistics as complete. Request payload text is capped at 256 KiB. CSV exports contain at most 10,000 metadata rows and report truncation with `X-Export-Truncated` when the cap or response byte limit is reached. API request bodies are limited to 1 MiB, and connection tests and data operations have a five-minute deadline.

The Go runtime uses a 256 MiB soft memory target. This is a runtime target, not an RSS guarantee or a machine benchmark. No unbounded result cache is used.

## Frontend development

Run the backend and Vite in separate PowerShell terminals. Set the same explicit token in each terminal; it must contain at least 32 characters. The backend needs an explicit `--port` here so Vite knows where to proxy. In serve mode Vite prints a link of the form `http://127.0.0.1:<vite port>/#t=<token>`; open that link. `AUDIT_API_TARGET` optionally overrides Vite's loopback proxy target (default `http://127.0.0.1:18765`).

Backend terminal:

```powershell
$env:AUDIT_SESSION_TOKEN = 'replace-with-a-local-token-of-at-least-32-chars'
..\..\.dev\toolchains\go\bin\go.exe run ./cmd/audit-explorer --demo --port 18765 --no-browser
```

Frontend terminal:

```powershell
$env:AUDIT_SESSION_TOKEN = 'replace-with-a-local-token-of-at-least-32-chars'
$env:AUDIT_API_TARGET = 'http://127.0.0.1:18765'
Set-Location web
npm run dev -- --host 127.0.0.1
```

Use the same token value in both terminals. The frontend dev server binds to loopback and does not disable API authentication. Never use a real database URI in browser-side code or Vite environment variables.

## Build and checks

`build.ps1` resolves Go from `PATH` or the workspace-local `.dev/toolchains/go/bin/go.exe`. Pass `-GoPath` to select a specific executable. It keeps Go caches under `.dev/toolchains/cache`, installs the isolated web dependencies with `npm ci`, runs the frontend check and production build, runs `go test ./...` and `go vet ./...`, and builds `dist/unravis.exe` with CGO disabled, plus a `dist/api-audit-explorer.exe` compatibility copy for one release.

To skip web dependency installation and rebuild only the Go executable using the existing `web/dist` assets:

```powershell
.\build.ps1 -SkipWeb
```




## Incident patterns and the ledger

The **Patterns** and **Ledger** buttons in the header open two side panels.

- **Patterns** reads the audit requests the app already reads for the selected sources and window, under the same time, concurrency and 7-day limits. It adds no new MongoDB query, and it never runs by itself: a run starts only when you press its button.
- Patterns counts stalls (requests of 45 seconds or longer), episodes (stalls separated by a quiet gap of at least 10 minutes), volleys (two or more stalls from one user starting within 1 second) and audit-insert stalls. A presence rate reads as k of n episodes.
- Each finding shows one evidence label: Symptom, Correlation or Strong correlation. It also shows n, a low-sample flag, the incomplete, pending and invalid counts, and coverage. A pattern is called recurring only over 3 or more independent episodes; one episode is shown as a single episode.
- **Ledger** is a local file of hypotheses, their states, references and notes. It holds no row data or identifiers. It never sets a state itself: you choose each state, and guards can only block "strong correlation" when its requirements are not met.
- Nothing here proves a cause. A finding describes what was recorded and how often it appeared, and a correlation is a clue to check, not a root cause.

## AI investigations

Local Codex/Claude investigations are available through the executable's --mcp mode. See [MCP.md](MCP.md) for tools, workflow, configuration and evidence limits. MCP reuses the running dashboard's connected sources and does not expose credentials, raw URLs, payloads, connection changes or database writes.


### Nearby activity around a request

In an API group's request history, choose **Nearby activity**. Set independent **Before** and **After** amounts in seconds, minutes, or hours, then apply the window. The timeline pins the selected request and shows API calls and background jobs across the investigation's selected sources, including requests known to overlap the window. The window includes the selected request's duration and may span up to 24 hours. Load more retrieves bounded pages, up to 500 displayed records.

Missing completion times, failed sources and incomplete pages remain visible. Activity happening at the same time is a clue, not proof of a cause; server clock differences can affect ordering.

### Request counts by individual API

After running a comparison, the request-volume histogram shows request starts per time slice, split by controller/action name. It follows the investigation sources, date range and filters, including records without usable response timings. The eight busiest names have separate colors; Other APIs preserves the remaining counts. Identical API names are combined across selected sources/types/outcomes.

Choose Window starts/ends, or click a bar, to inspect an interval using the existing chart boundaries. The exact-count table supports search, pagination and Show only for any API. Full investigation window resets the view. These controls reuse the current result and make no extra database calls. Counts show starts rather than concurrent activity; failed-source coverage is shown explicitly. Run the comparison again after changing investigation inputs.

### Traffic versus slowness

The traffic/latency scatter pairs request starts per comparison time window with pooled P95 (default), median, or longest response time. Pooled histograms are merged before computing percentiles; API percentiles are never averaged. Select the response-time metric and logarithmic/linear scale locally. Window resolution follows the comparison chart interval.

A red window has at least one measured request longer than the editable duration threshold (initially1000ms). This initial setting is an investigation aid, not an agreed performance target. Baseline change stays contextual in the slowest-request table so tiny-but-multiplied durations are not automatically treated as unhealthy. The orange line is the fraction of measured complete windows with a known threshold exceedance within doubling request-count ranges. Hover or expand exact windows for counts/denominators. Partial boundary windows and windows with no usable timings are excluded from the fraction; mixed API workload and incomplete source/timing coverage can confound the relationship.

Click a dot or Inspect window to read up to50 globally slowest requests for that clipped interval; the Load50 button uses the full investigation when no window is selected. Ranking uses the selected full-request/API-processing metric across selected sources, with one bounded metadata read per source, not a sorted newest-page sample. The threshold box is applied by the server as an inclusive minimum, and results arrive 50 at a time with Load more: a cursor on duration, start time, source and record ID that stays stable across pages and is bound to the query and minimum. The minimum can instead be the P95 or P99 of the selected API group, converted to milliseconds in the browser from its histogram and labelled as a histogram estimate (P95 needs at least 20 samples, P99 at least 100). The API table also lists P50, P90 and P99. The legacy metadata scan retains existing record/byte limits. URL and payload bodies are excluded. Partial-source results remain explicit. Nearby activity opens the existing surrounding API/job timeline. Request time is additive: the audit phase (AuditCreateTime, writing the audit row before the API runs) and the API phase (ApiTime) add up to the total latency. The payload write (PayloadRecordTime) happens after the response is sent, so it is shown separately as "after response, not latency" and is never part of the total. Each API group shows its current-period mean audit and API time as a bar, with the change versus the reference period in its tooltip; a phase mean divides by the requests that carry that phase. Earlier P95 ratios use an exact source/API/kind/outcome match with at least20 reference timings.

Design rationale: [Google SRE monitoring](https://sre.google/workbook/monitoring/) recommends examining traffic and latency percentiles; [Prometheus histogram guidance](https://prometheus.io/docs/practices/histograms/) explains merging histogram observations and using duration thresholds. Neither prescribes our editable one-second starting value. Correlation is evidence to investigate, not proof of root cause.

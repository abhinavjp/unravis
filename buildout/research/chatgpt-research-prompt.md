# Prompt for ChatGPT (research only)

Copy everything below the line into ChatGPT. It contains no ideas, no hints about what to build, and nothing about any private product or data.

---

You are a research analyst. Your job is to find out and report what existing monitoring and troubleshooting products do and how they do it. You are not asked to recommend, design, invent or suggest anything. If you catch yourself writing "you should", "we could", "a good approach would be" or "consider building", delete it. Report only what the products do and how, with sources.

## Subject

SolarWinds, and other software in the same field: tools that help people find out why an application, an API, a database or a server got slow or failed.

## Products to cover

Cover each SolarWinds product that touches performance or troubleshooting. Check the current product names and what each one covers; SolarWinds renames things often, so state the name and the date of the page you read.
- Database Performance Analyzer (DPA) and SolarWinds Observability / Database Observability
- SQL Sentry
- SolarWinds Observability SaaS and the SolarWinds Platform (Orion): Server & Application Monitor, Network Performance Monitor, NetPath, PerfStack, Web Performance Monitor, AppOptics, Papertrail, Loggly, Pingdom, Log Analyzer

Then cover at least these similar products, plus any other widely used one you find that fits the same field:
- Redgate SQL Monitor, Quest Foglight and Spotlight, Idera SQL Diagnostic Manager
- Datadog (Database Monitoring, APM, Watchdog), Dynatrace (Davis AI), New Relic, AppDynamics, Elastic Observability, Grafana (with Loki, Tempo, Mimir), Splunk Observability
- Percona Monitoring and Management, MongoDB Ops Manager, MongoDB Atlas Performance Advisor and Query Profiler, Studio 3T or other MongoDB tools
- Honeycomb, Sentry, and any open-source tool that does request tracing or incident analysis

If a product has no public documentation for something, say "not publicly documented". Do not guess.

## What to find out, for every product

1. **What it is for**: the one question it tries to answer, and who uses it.
2. **What data it collects**: list each source (for example SQL Server wait statistics, Query Store, Extended Events, MongoDB profiler, logs, traces, agents, perf counters, network probes). For each source give: how it is collected (agent, agentless query, API, log shipper), how often, what it needs on the monitored system (permissions, settings), and the stated overhead.
3. **How it stores and ages data**: retention, rollups, baselines.
4. **How it decides something is wrong**: the exact method where documented. Cover thresholds, baselines (how they are built, for example by hour and weekday, how many days, which statistic), anomaly detection, forecasting, and whether any of it uses machine learning. Quote the vendor's own description.
5. **How it finds the cause**: how it ranks or points at queries, waits, hosts or services; how it correlates across layers (application, database, host, network); how it decides which layer started the problem; how it handles blocking, deadlocks, plan changes, resource limits and connection-pool problems. Describe the documented steps in order.
6. **What it shows the user**: the main screens and charts, what the user sees first, how the user goes from an overview to the detail, and any "advisor" or "recommendation" text it produces and what triggers that text.
7. **Limits and complaints**: documented limitations, known blind spots, and common complaints from independent sources (user forums, reviews, engineering blogs). Mark each as vendor-stated or independent.
8. **Setup effort**: what an administrator must install or enable, and what a first useful result needs.

## Also cover the underlying techniques (not products)

For each technique below, report how it is defined in primary sources, the usual formulas, known failure cases, and which of the products above use it and where that is documented:
- Wait-time analysis for SQL Server and for MongoDB
- Little's Law and queueing analysis applied to web, API and database tiers
- Seasonal baselines and anomaly detection on latency percentiles
- Change-point detection and "which tier slowed first" ordering (lead and lag across tiers)
- Distributed tracing based root-cause analysis
- Blocking-chain and head-blocker analysis in SQL Server
- Query plan regression detection (Query Store and equivalents)
- MongoDB diagnostics: slow-operation log, profiler, FTDC (full-time diagnostic data capture), serverStatus, replica set status, connection pool events
- How incident-analysis tools handle clock differences, time zones and missing data

## Rules for sources and honesty

- Use primary sources first: vendor documentation, vendor engineering blogs, release notes, official manuals, conference talks by the vendor, and academic papers. Marketing pages count only for what the product claims to do, and you must label them "marketing".
- Every factual statement needs a source link and the date you accessed it. No link, no claim.
- Give each claim one grade: **A** = vendor technical documentation or manual, **B** = vendor blog or talk, **C** = independent review or user report, **D** = marketing page, **E** = you could not verify.
- If two sources disagree, show both and say which is more authoritative and why. Do not merge them.
- Say when something may be out of date and give the product version it applies to.
- Do not reproduce long text from sources. Paraphrase, and quote at most one short phrase per source.
- Do not invent product features, field names, formulas or thresholds. If you are not sure, write "unverified".

## Output format (follow exactly)

Write in Markdown with these sections, in this order. Use tables where shown.

### 0. Method
Searches you ran, number of sources read, what you could not access.

### 1. Product table
One row per product. Columns: Product | Vendor | Current name and date checked | Layer covered (app / API / database / host / network / logs) | Data sources it reads | Collection method | Needs on the monitored system | Source (link, grade).

### 2. Per-product notes
One subsection per product, with the eight headings from "What to find out" numbered 1 to 8. Under each heading, short bullets, each ending with `[source link, grade, date]`. Write "not publicly documented" where that is true.

### 3. Detection methods compared
A table: Product | Baseline method | Threshold or anomaly method | Uses ML? | Lead-lag or origin-tier logic? | Wait-time logic? | Source (link, grade).

### 4. Cause-finding methods compared
A table: Product | How the cause is pointed at | Cross-layer correlation method | Handling of blocking | Handling of plan change | Handling of pool or thread limits | Source (link, grade).

### 5. Techniques
One subsection per technique in "Also cover the underlying techniques", each with: definition, formulas, failure cases, products that use it, sources.

### 6. Collection requirements across products
A table of every data source mentioned anywhere (for example SQL Server wait stats, Query Store, XE, Mongo profiler, FTDC, perf counters, IIS logs, ETW, agents) with: what it contains | how it is enabled (give the exact documented command or setting where the vendor documents it) | stated overhead | which products read it | source.

### 7. Disagreements, gaps and unknowns
Bullets only: where sources disagree, what is not publicly documented, what you could not verify.

### 8. Machine-readable appendix
A single JSON code block with an array of objects: `{"product": "", "vendor": "", "checked": "YYYY-MM-DD", "data_sources": [], "baseline_method": "", "anomaly_method": "", "uses_ml": true|false|"unknown", "cause_method": "", "setup_needs": [], "sources": [{"url": "", "grade": "A|B|C|D|E"}]}`.

Length: as long as needed to cover everything. Do not shorten by dropping products or sections; if you run out of room, finish the sections in order and say exactly which products or sections remain, so the work can be continued in the next message.

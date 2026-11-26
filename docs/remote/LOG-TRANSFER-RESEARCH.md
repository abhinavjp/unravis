# Getting logs and Blackbox data off a server: what works, what we should do

Question from nachi (2026-11-26): what is the most efficient way to get logs and Blackbox data off a server, especially above 2 GB? Compress or not, take everything or part of it, load in the background or up front?

**Main path.** Unravis normally runs on the server itself and reads files in place. Nothing is transferred. Everything below is for the **fallback**: a PC pulling through a Salt master.

**Status of the claims.** Sources are linked. Items marked *unverified* come from memory or one forum post and must be tested on the real master before the design relies on them. Nothing here has been run against a real Salt master yet.

## 1. What other tools do

| Tool | Approach | Lesson for us |
|---|---|---|
| Velociraptor offline collector | Collects a chosen set of artifacts into one zip (optionally encrypted), then uploads it. Collect near the machine, so you do not pay for bandwidth to the cloud. 0.75 adds resumable collections and lets server-side compression be turned off. Broad collections need raised resource limits. | Pull a **bundle** chosen by purpose, not the disk. Make it resumable. Let compression be switched off. [collection data](https://docs.velociraptor.app/docs/deployment/offline_collections/collection_data/), [0.75 notes](https://docs.velociraptor.app/blog/2025/2025-08-30-release-notes-0.75), [bulk collection](https://docs.velociraptor.app/docs/file_collection/bulk/) |
| KAPE / Kroll triage | Targeted collection of the artifacts that matter, in minutes, instead of a full image. Under about 10% of the data usually matters. KAPE has a commercial-use licence limit, so we copy the idea only. | Take the **investigation window**, not everything. [Kroll](https://www.kroll.com/en/publications/cyber/kroll-artifact-parser-extractor-kape) |
| Filebeat / Fluent Bit / Splunk forwarder | Remember a per-file offset, send only new bytes, buffer, and slow down when the receiver is behind. At-least-once delivery. | **Offsets and backpressure.** Resume by byte offset, never restart a file. [Filebeat](https://www.elastic.co/guide/en/beats/filebeat/current/how-filebeat-works.html), [Fluent Bit](https://docs.fluentbit.io/manual/administration/backpressure) |
| Loki | Keeps a small index of labels and fetches the data chunks only when a query needs them. | **Index first, details on demand.** [labels guide](https://grafana.com/docs/loki/latest/get-started/labels/) |
| HTTP Range and If-Range | Resume a download at a byte offset. If the file changed, the server sends the whole file again (or a 412 with If-Match). | Tie each resume to the file's size and time, so a changed file restarts cleanly. [MDN If-Range](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/If-Range) |
| rsync over ssh | `--partial` and `--timeout` give resume. Do not stack `rsync -z` on `ssh -C`, because compressing twice wastes CPU. | Compress in **one** place only. [rsync over ssh](https://linuxblog.io/rsync-over-ssh/) |

## 2. Compression

- **It uses server CPU.** Log text compresses well (roughly 3.5 to 4 to 1), so the trade is usually worth it on a slow link and not worth it on a fast one.
- **Pick a fast algorithm at a low level.** One published test has zstd level 1 at about 257 MB/s with ratio 3.57, against gzip -6 at about 22 MB/s with 3.71 ([test](https://tech-insider.org/zstd-vs-gzip-vs-lz4-compression-benchmarks-2026/)). Higher zstd levels give small gains for large CPU cost ([depot](https://depot.dev/blog/github-actions-cache-zstd)). So zstd 1 to 3, or deflate fast, run with `nice` (low priority). One rclone user found gzip -9 beat zstd -4 on ratio ([forum](https://forum.rclone.org/t/compress-findings/53859)), so *test on our real logs* with `zstd -b1 -e9`.
- **Adaptive modes exist but are risky.** `zstd --adapt` changes level with I/O conditions and can stall with two or more threads ([man page](https://man7.org/linux/man-pages/man1/zstd.1.html)). Not for the first version.
- **Skip files that are already compressed** (zip, gz, zstd, the Blackbox bundle). Compressing them again burns CPU and gains nothing.
- **Compress once.** At the source, as a single stream, then send. Not at both the file and the transport.
- **The Blackbox bundle zip is already compressed on the server**, once, by the Blackbox thread's collector. Servers just moves it.
- **Our link is Salt, not raw SSH.** Salt returns data as text, so we base64 it, which adds about 33%. Compressing first is worth even more here.
- **Offer a switch.** Auto (default), Always, Never. Never exists for a busy server or a fast network, as Velociraptor does.

## 3. Taking part of it

This saves the most. A case already has a window (the investigation) and often a reference range (a normal period before it). Pulling those two ranges is a small slice of a multi-GB folder. Recordings, IIS logs and event logs are all named or indexed by time, so a manifest of file names, sizes and time ranges is cheap and lets us choose before any bytes move.

## 4. What Salt can and cannot do

- **Our current design** reads in chunks through `cmd.run`, base64 encoded, 48 KiB per job by default. 2 GB is about 43,000 jobs, and each leaves an entry in the master's job cache. That is acceptable for hundreds of MB and **not** for multiple GB. This arithmetic is mine, not a measurement.
- **`cp.push`** moves a file from the minion to the master, but needs the master's `file_recv` setting, which is off by default ([master config](https://docs.saltproject.io/en/latest/ref/configuration/master.html)). A `file_recv_max_size` limit exists. Its default (I recall about 100 MB) is *unverified*.
- **Event size** is capped by `max_event_size`. *Unverified* how that interacts with large `cmd.run` replies on this master.
- Ansible's Salt connection plugin uses `hashutil.base64_decodefile` and `cp.get_file_str` for transfers ([docs](https://docs.ansible.com/ansible/latest/collections/community/general/saltstack_connection.html)), which is the same chunk pattern with the same limits.
- **Practical rule:** compress on the server first, then chunk. For anything over a few hundred MB, prefer a file share or `cp.push` if the admin enables it, and say so plainly in the screen.

## 5. Eager or lazy

| Style | Good for | Bad for |
|---|---|---|
| Eager, all up front | Small data, a case you must open now | Anything big. You wait before you can look. |
| Lazy, on demand | Rare details | The user waits at the moment they click. |
| **Index first, window eager, rest lazy in the background** | Cases with a clear time window | Needs a queue, resume, and a clear indicator |

## 6. Recommendation

1. **Manifest first.** List files with size and time range (a few KB). Show it before any copying. This is the Loki lesson.
2. **Eager for the case.** Pull the investigation window plus the reference range, in time order, so the timeline can open as soon as the first part lands. Defaults come from the case, and stay editable.
3. **Lazy for the rest.** Back-fill the remaining days in the background, newest first, as a low-priority queue that pauses when the user starts another pull. It never touches the server's CPU more than the chosen setting allows.
4. **Compression: Auto.** Text logs: compress once on the server with zstd level 1 to 3 (or deflate fast if zstd is not installed), run at low priority. Zip, gz and bundles: send as they are. User can force Always or Never. Estimate size and time before starting.
5. **Always** check the hash per file, and resume by byte offset. A changed file restarts, never half-resumes.
6. **Say the Salt limits plainly.** Above a size we measure on the real master, the screen says "this will take a long time through Salt; ask your admin for a share, or run Unravis on the server".
7. **Main path first.** Unravis on the server reads in place. Nothing in this document applies there.

## 7. What to prototype and what to measure

**Prototype (v4, done in `web/prototype/servers-PROTOTYPE.html`):** case window plus reference range in "How much"; manifest-first step; compression choice with estimates; background back-fill indicator; honest large-pull warning; copy-ready access check.

**Measure on the real Salt master before building more** (none of this is verified yet):

1. Largest `cmd.run` reply that returns reliably, and the job time per chunk (start the plan's 5 MB test).
2. Job-cache growth over 1,000 chunks.
3. `file_recv` and `file_recv_max_size` on this master, and whether the admin will allow `cp.push`.
4. `zstd -b1 -e9` against a real day of Blackbox, IIS and event-log files, to pick the level.
5. Server CPU while compressing at `nice` 19.

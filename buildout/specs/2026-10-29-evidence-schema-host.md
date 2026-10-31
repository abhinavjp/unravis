# D1-Host: host resource evidence card scenarios

## 1. Host evidence rows

Unit is stated per row. Verdict test is in plain words.

| id | label | verdict test (plain words) | collected today? | needs which permission |
|---|---|---|---|---|
| host-cpu | CPU busy time | Green under 70% CPU; amber 70-85% or clearly rising; red 90%+ held for 5 min, or I/O-wait/steal dominating. Unit: percent of host CPU. | yes (`HostCPU`) | read Windows perf counters / WMI (PerfMon, node_exporter, or `sar` on Linux) |
| host-mem | Memory available / committed | Green with headroom and no paging; amber when headroom is low; red on thrash or OOM. Unit: bytes free, bytes committed. | partial (`HostMemAvailable`, `HostMemCommitted`; no swap series) | read perf counters (`Memory` object) |
| host-pages | Page faults | Green near-zero paging; amber occasional; red sustained paging. Unit: faults per second. | yes (`HostPages`) | read perf counter `Memory\Pages/sec` |
| host-disk | Disk latency and queue | Green under 10 ms on SSD, queue near 0; amber both rising; red sustained queue plus high latency. Unit: ms/transfer, queue depth. | yes (`HostDiskSecPerTx`, `HostDiskQueue`) | read perf counters (`PhysicalDisk`/`LogicalDisk`) |
| host-net | Network throughput and errors | Green errors/drops near zero; amber occasional; red sustained with impact. Unit: bytes/sec; retransmits/errors in counts. | partial (`HostNetBytes`; no retransmits/drops) | read NIC perf counters; errors need `netstat -s` or NIC counters |
| host-proc-cpu | CPU by process | Green one process within its share; amber one process climbing; red one process saturating its cores. Unit: percent of CPU per process. | yes (`ProcCPU`) | read each process; other users' processes may need elevation |
| host-proc-mem | Memory by process | Green working set stable; amber one process growing; red one process exhausting memory. Unit: bytes working set / private. | yes (`ProcWS`, `ProcPrivate`) | read each process; other users' processes may need elevation |
| host-proc-handles | Threads and handles by process | Green flat; amber slow climb; red unbounded growth (leak). Unit: counts. | yes (`ProcThreads`, `ProcHandles`) | read each process; some fields need elevation |
| host-gc | GC pauses and thread-pool queue | Green short pauses and empty queue; amber pauses or queue rising; red long pauses or a growing queue (starvation). Unit: ms pause, queue count. | yes (`NetGCPause`, `CLRGCTime`, `CLRGen2`, `NetTPQueue`) | run `dotnet-counters` (read) against the app |
| host-runqueue | CPU run queue | Green no waiters; amber transient; red sustained runnable queue. Unit: threads waiting for CPU. | no | would need `System\Processor Queue Length` / `vmstat r` |
| host-tcp | TCP connection counts | Green headroom; amber approaching a limit; red exhaustion with waits. Unit: open connections. | no | would need `netstat`/`GetTcpStatistics` |
| host-swap | Swap in / out | Green zero; amber occasional paging; red active thrash. Unit: pages/sec. | no | would need swap counters (`vmstat si/so`, Paging File) |

Notes: the collector reads host CPU, memory available/committed, pages/sec, disk latency/queue, network bytes, and per-process CPU/memory/handles/threads (plus .NET GC and thread-pool queue). It does **not** read a host run queue, TCP connection counts, swap paging, network retransmits, or host iowait/steal directly. Sample cadence is 15-60 s, so every collected row is `sampled: true`.

## 2. Scenarios

12 fixtures, 3 per colour. Window mapping: `win:"normal"` is t 0-50 (08:00-08:59), `win:"incident"` is t 60-110 (09:00-09:59); demo incident 26 Oct 09:00-10:00 UTC.

- **ok:** GS-01 CPU normal; GS-02 disk fast, no queue; GS-03 memory has headroom.
- **watch:** GS-04 CPU 62%→76% (23% worse, past amber half-gap, under red gap); GS-05 disk 8→20 ms rising; GS-06 network throughput doubled, retransmits not collected (partial).
- **bad:** GS-07 CPU 92% held 6 min (named saturation: 90%+ for 5 min); GS-08 memory near-zero with swap thrash and one OOM kill; GS-09 disk 53 ms with a sustained queue of ~13.
- **none:** GS-10 run queue not collected; GS-11 TCP connection counts not collected; GS-12 swap activity not collected.

Colour test used: red when the incident value is at least 30% and 15 points worse than reference (or a named saturation level); amber at half of that (15% and 7.5 points); green otherwise; gray when not collected.

## 3. Legend wording

- **Green (ok):** same as generic (within normal range, ruled out).
- **Amber (watch):** same as generic (worse than reference but under the red line; watch it).
- **Red (bad):** for hosts: the machine itself is saturated (CPU above 90% for 5 minutes, memory thrashing, or a sustained disk queue), not just busy.
- **Gray (none):** same as generic, but the host reason is always "not collected yet"; absence is not a clean bill of health.

## 4. Fixture JSON


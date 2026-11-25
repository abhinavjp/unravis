// A made-up Windows Performance Monitor export (PDH-CSV 4.0, what relog writes) for the importer "win-counters": host CPU and available memory every 15 s
// from 08:45 to 09:00 UTC on the demo's day (20 Oct 2026), a bias of 0, with a stretch of high CPU in the middle. Used by the e2e specs and the UI audit.
import { HOST } from './api';

export function perfmonCsv(host: string = HOST): string {
  const name = host.toUpperCase();
  const head = `"(PDH-CSV 4.0) (UTC)(0)","\\\\${name}\\Processor(_Total)\\% Processor Time","\\\\${name}\\Memory\\Available MBytes"`;
  const rows: string[] = [head];
  const start = Date.UTC(2026, 9, 20, 8, 45, 0);
  const two = (n: number) => String(n).padStart(2, '0');
  for (let i = 0; i < 60; i += 1) {
    const t = new Date(start + i * 15_000);
    const stamp = `${two(t.getUTCMonth() + 1)}/${two(t.getUTCDate())}/${t.getUTCFullYear()} ${two(t.getUTCHours())}:${two(t.getUTCMinutes())}:${two(t.getUTCSeconds())}.000`;
    const cpu = i > 20 && i < 30 ? 96.5 : 18 + (i % 7);
    rows.push(`"${stamp}","${cpu}","${4096 - i * 10}"`);
  }
  return rows.join('\r\n') + '\r\n';
}

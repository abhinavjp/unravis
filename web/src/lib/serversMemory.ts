/**
 * What this PC remembers about each machine, per saved server: when it last answered a check, when files were last pulled from it, whether
 * it had a Blackbox recorder, and which file pattern each folder needs. It lives in this browser only and holds no secret and no log text.
 * Every function is pure over a plain object; the two storage helpers take the storage as an argument so a test can pass a fake.
 */

export const MEMORY_KEY = 'unravis.servers.memory.v1';

export interface MachineMemory {
  /** ISO time of the last check that reached the machine. */
  connected?: string;
  /** ISO time of the last pull that finished. */
  pulled?: string;
  /** True when the last check found a Blackbox recorder answering. */
  blackbox?: boolean;
  /** Folder path to the file pattern the recorder named for it. */
  patterns?: Record<string, string>;
}

export type Memory = Record<string, Record<string, MachineMemory>>;

/** The browser storage, or null where there is none (tests, private modes that refuse it). */
export function browserStorage(): StorageLike | null {
  try { return typeof localStorage === 'undefined' ? null : localStorage; } catch { return null; }
}

export interface StorageLike { getItem(key: string): string | null; setItem(key: string, value: string): void }

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const isTime = (v: unknown): v is string => typeof v === 'string' && Number.isFinite(Date.parse(v));

/** Keep only what the shape allows, so a hand-edited or old entry cannot break the screen. */
export function cleanMemory(raw: unknown): Memory {
  const out: Memory = {};
  if (!isRecord(raw)) return out;
  for (const [server, machines] of Object.entries(raw)) {
    if (!isRecord(machines)) continue;
    const kept: Record<string, MachineMemory> = {};
    for (const [machine, m] of Object.entries(machines)) {
      if (!isRecord(m)) continue;
      const entry: MachineMemory = {};
      if (isTime(m.connected)) entry.connected = m.connected;
      if (isTime(m.pulled)) entry.pulled = m.pulled;
      if (typeof m.blackbox === 'boolean') entry.blackbox = m.blackbox;
      if (isRecord(m.patterns)) {
        const patterns: Record<string, string> = {};
        for (const [dir, pattern] of Object.entries(m.patterns)) if (typeof pattern === 'string') patterns[dir] = pattern;
        if (Object.keys(patterns).length) entry.patterns = patterns;
      }
      kept[machine] = entry;
    }
    out[server] = kept;
  }
  return out;
}

export function loadMemory(storage: StorageLike | null | undefined): Memory {
  try {
    const text = storage?.getItem(MEMORY_KEY);
    return text ? cleanMemory(JSON.parse(text)) : {};
  } catch { return {}; }
}

/** A full or blocked store is not an error the person can fix, so saving never throws. */
export function saveMemory(storage: StorageLike | null | undefined, memory: Memory): void {
  try { storage?.setItem(MEMORY_KEY, JSON.stringify(memory)); } catch { /* nothing to do */ }
}

export function machineMemory(memory: Memory, server: string, machine: string): MachineMemory {
  return memory[server]?.[machine] ?? {};
}

function update(memory: Memory, server: string, machine: string, change: (m: MachineMemory) => MachineMemory): Memory {
  const before = machineMemory(memory, server, machine);
  return { ...memory, [server]: { ...(memory[server] ?? {}), [machine]: change({ ...before }) } };
}

export const recordCheck = (memory: Memory, server: string, machine: string, at: Date, blackbox: boolean | undefined): Memory =>
  update(memory, server, machine, (m) => ({ ...m, connected: at.toISOString(), ...(blackbox === undefined ? {} : { blackbox }) }));

export const recordPull = (memory: Memory, server: string, machine: string, at: Date): Memory =>
  update(memory, server, machine, (m) => ({ ...m, pulled: at.toISOString() }));

/** Remember the pattern the recorder named for a folder. An empty pattern is not remembered. */
export function recordPatterns(memory: Memory, server: string, machine: string, found: readonly { dir: string; pattern: string }[]): Memory {
  const usable = found.filter((f) => f.dir && f.pattern);
  if (!usable.length) return memory;
  return update(memory, server, machine, (m) => ({ ...m, patterns: { ...(m.patterns ?? {}), ...Object.fromEntries(usable.map((f) => [f.dir, f.pattern])) } }));
}

export function patternFor(memory: Memory, server: string, machine: string, dir: string): string {
  return machineMemory(memory, server, machine).patterns?.[dir] ?? '';
}

/** Drop what is remembered about a server that is gone. */
export function forgetServer(memory: Memory, server: string): Memory {
  const { [server]: _gone, ...rest } = memory;
  return rest;
}

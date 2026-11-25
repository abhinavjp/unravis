import type { SavedSqlConnection } from './types';
import type { Settings, SqlSetupAnswer } from './blackbox';
import { SQLC_TEXT } from './copy/blackboxSqlCopy';

export const SQL_PROFILES = ['standard', 'everything', 'deep'] as const;
export type SqlProfile = (typeof SQL_PROFILES)[number];

/** The profile the installed recorder runs with. A missing or unknown value reads as standard. */
export function profileOf(s: Settings | undefined): SqlProfile {
  const p = s?.sql?.profile;
  return p === 'everything' || p === 'deep' ? p : 'standard';
}

/** The connections the SQL service can use: a SQL login whose password is kept. Windows sign-in or a missing password cannot run unattended. */
export const loginChoices = (all: SavedSqlConnection[]): SavedSqlConnection[] => all.filter((c) => c.auth === 'sql' && c.hasPassword);

export const QUERY_TEXT_SIZES = [500, 1000, 2000, 4000] as const;
export const DEFAULT_QUERY_TEXT_SIZE = 1000;

/** True when the installed recorder keeps query text: the deep profile with the flag. Anything else keeps none. */
export const keepsQueryText = (s: Settings | undefined): boolean => profileOf(s) === 'deep' && s?.sql?.queryText === true;

/** The size cap in force, one of the offered sizes. */
export function queryTextSizeOf(s: Settings | undefined): number {
  const n = s?.sql?.queryTextMaxChars;
  return (QUERY_TEXT_SIZES as readonly number[]).includes(n ?? 0) ? (n as number) : DEFAULT_QUERY_TEXT_SIZE;
}

/** The settings with the chosen profile, the rest untouched. Standard is written as no block, so a plain install stays plain. */
export function withProfile(s: Settings, profile: SqlProfile, text: { on: boolean; size: number } = { on: false, size: DEFAULT_QUERY_TEXT_SIZE }): Settings {
  const next: Settings = { ...s };
  const block: NonNullable<Settings['sql']> = { ...s.sql, profile };
  // Query text exists only in the deep profile: any other choice drops the flag and the size, so it can never ride along unnoticed.
  if (profile === 'deep' && text.on) { block.queryText = true; block.queryTextMaxChars = text.size; }
  else { delete block.queryText; delete block.queryTextMaxChars; }
  const bare = profile === 'standard' && !(block.extras && block.extras.length);
  if (bare) delete next.sql;
  else next.sql = block;
  return next;
}

/** One line for a finished setup. */
export const setupLine = (a: SqlSetupAnswer): string => SQLC_TEXT.done({ installed: a.installed, started: a.started });

/** The one line about the SQL service next to the switch: from the service manager only, never a claim about the login. */
export function sqlStatusLine(pc: { installed: boolean; sqlInstalled?: boolean; sqlRunning?: boolean }): string {
  if (!pc.installed) return '';
  if (!pc.sqlInstalled) return SQLC_TEXT.statusNotInstalled;
  return pc.sqlRunning ? SQLC_TEXT.statusRunning : SQLC_TEXT.statusStopped;
}

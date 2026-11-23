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

/** The settings with the chosen profile, the rest untouched. Standard is written as no block, so a plain install stays plain. */
export function withProfile(s: Settings, profile: SqlProfile): Settings {
  const next: Settings = { ...s };
  if (profile === 'standard' && !(s.sql?.extras && s.sql.extras.length)) delete next.sql;
  else next.sql = { ...s.sql, profile };
  return next;
}

/** One line for a finished setup. */
export const setupLine = (a: SqlSetupAnswer): string => SQLC_TEXT.done({ installed: a.installed, started: a.started });

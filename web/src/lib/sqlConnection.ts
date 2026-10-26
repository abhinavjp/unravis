import type { SavedSqlConnection, SqlAuth, SqlConnectionInput, SqlEncrypt, SqlTestRequest } from './types';

/**
 * What the SQL Server form holds while it is open. The password lives here and in the request, never in the wizard draft.
 * keptId names the kept entry the form was filled from; its saved password may be used for a test only while the server, sign-in method and user are unchanged.
 */
export interface SqlForm {
  label: string; server: string; auth: SqlAuth; username: string; password: string; rememberPassword: boolean;
  database: string; encrypt: SqlEncrypt; trustServerCertificate: boolean;
  keptId: string; keptServer: string; keptUser: string; keptAuth: SqlAuth;
}

export const blankSqlForm = (): SqlForm => ({
  label: '', server: '', auth: 'sql', username: '', password: '', rememberPassword: false, database: '', encrypt: 'mandatory', trustServerCertificate: false,
  keptId: '', keptServer: '', keptUser: '', keptAuth: 'sql'
});

/** The request that tests the form's connection. A typed password always wins; otherwise the kept one is used, on the server, when nothing it was kept for has changed. */
export function sqlTestInput(form: SqlForm): SqlTestRequest {
  const sql = form.auth === 'sql';
  const sameLogin = form.keptId !== '' && form.keptServer === form.server.trim() && form.keptUser === form.username.trim() && form.keptAuth === form.auth;
  return {
    server: form.server.trim(), auth: form.auth, username: sql ? form.username.trim() : '', password: sql ? form.password : '',
    database: form.database.trim(), encrypt: form.encrypt, trustServerCertificate: form.trustServerCertificate,
    ...(sql && form.password === '' && sameLogin ? { savedId: form.keptId } : {})
  };
}

/** True when a kept password will be used for the test (nothing typed, same login). */
export const usesKeptPassword = (form: SqlForm): boolean => sqlTestInput(form).savedId !== undefined;

/** The connection fields that decide which databases a test lists. A change to any of them makes an earlier list stale. */
export const sqlConnectionKey = (form: SqlForm): string =>
  [form.server.trim(), form.auth, form.auth === 'sql' ? form.username.trim() : '', form.encrypt, form.trustServerCertificate ? '1' : '0'].join('\u0001');

export type SqlFormProblem = 'server' | 'username';

/** The first thing wrong with the form, or null. The server name itself is checked by the address parser. */
export function sqlFormProblem(form: SqlForm): SqlFormProblem | null {
  if (form.server.trim() === '') return 'server';
  if (form.auth === 'sql' && form.username.trim() === '') return 'username';
  return null;
}

/** The request body. Windows login sends no user name or password, and a password goes only when "Remember password" is ticked. */
export function sqlInput(form: SqlForm): SqlConnectionInput {
  const sql = form.auth === 'sql';
  return {
    label: form.label.trim(), server: form.server.trim(), auth: form.auth,
    username: sql ? form.username.trim() : '', password: sql && form.rememberPassword ? form.password : '',
    rememberPassword: sql && form.rememberPassword, database: form.database.trim(), encrypt: form.encrypt,
    trustServerCertificate: form.trustServerCertificate
  };
}

/** Fills the form from a saved entry. The password is not returned by the server, so it stays blank. */
export function formFromSaved(saved: SavedSqlConnection): SqlForm {
  return {
    label: saved.label === saved.server ? '' : saved.label, server: saved.server, auth: saved.auth, username: saved.username, password: '',
    rememberPassword: saved.hasPassword, database: saved.database, encrypt: saved.encrypt, trustServerCertificate: saved.trustServerCertificate,
    keptId: saved.hasPassword ? saved.id : '', keptServer: saved.server, keptUser: saved.username, keptAuth: saved.auth
  };
}

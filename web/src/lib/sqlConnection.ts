import type { SavedSqlConnection, SqlAuth, SqlConnectionInput, SqlEncrypt } from './types';

/** What the SQL Server form holds while it is open. The password lives here and in the request, never in the wizard draft. */
export interface SqlForm {
  label: string; server: string; auth: SqlAuth; username: string; password: string; rememberPassword: boolean;
  database: string; encrypt: SqlEncrypt; trustServerCertificate: boolean;
}

export const blankSqlForm = (): SqlForm => ({
  label: '', server: '', auth: 'sql', username: '', password: '', rememberPassword: false, database: '', encrypt: 'mandatory', trustServerCertificate: false
});

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
    rememberPassword: saved.hasPassword, database: saved.database, encrypt: saved.encrypt, trustServerCertificate: saved.trustServerCertificate
  };
}

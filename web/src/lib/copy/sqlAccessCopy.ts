/** Words for the Blackbox access check on a SQL Server login (screen 54, SQL connection form). Plain words, no causes named. */
export const ACCESS_TEXT = {
  button: 'Check Blackbox access',
  checking: 'Checking...',
  heading: 'Blackbox access check',
  note: 'The check only reads. It changes nothing on the server. Unravis never runs a script for you.',
  needPassword: 'Type the password to check this login.',
  failed: 'The check could not run. Try again.',
  verdict: {
    ready: 'Ready. The Blackbox can read this server with this login.',
    'needs-grant': 'One step left. Run the script below on the server, then check again.',
    'too-many-rights': 'This login can do more than the Blackbox needs. Use a login that can only read server state. The script below makes one.',
    unreadable: 'The check could not finish. Fix the line marked Failed, then check again.'
  } as Record<string, string>,
  state: { passed: 'Passed', failed: 'Failed', warning: 'Warning', skipped: 'Not checked' } as Record<string, string>,
  scriptLabel: 'Script to run in SSMS as an admin',
  copy: 'Copy script',
  copied: 'Copied.',
  copyFailed: 'Select the script and copy it by hand.',
  writable: (names: string[], count: number): string =>
    'Databases this login can change data in: ' + names.join(', ') + (count > names.length ? ' and more' : '') + '.'
};

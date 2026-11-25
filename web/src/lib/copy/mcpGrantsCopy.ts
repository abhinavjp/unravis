import type { LintEntry } from './copylint';

/**
 * Every fixed sentence of the AI access screen (BL-347). Pure: no DOM, no fetch.
 * Rules: plain words; the host's own words (name, version, program path) are never part of a sentence here, they are shown apart and
 * labelled "reported by the host"; nothing is allowed until the person presses Approve; the stop is one button; say plainly that results
 * the host sends on cannot be taken back.
 */
export const MCP_TEXT = {
  open: 'AI access',
  title: 'AI access',
  closeLabel: 'Close AI access',
  intro: 'AI programs, such as an assistant in a chat app or an editor, can ask Unravis to read your audit data. Nothing is allowed until you approve it here.',
  loading: 'Reading the list…',
  refresh: 'Refresh',
  refreshing: 'Refreshing…',
  loadFailed: 'The list could not be read.',
  retry: 'Try again',

  // Modes without approvals
  legacyTitle: 'Approvals are not used in this run',
  legacyBody: 'This run has no approval store. An AI program that is started with the --mcp option reads through the older local link, and nothing here controls it. The Windows app asks for your approval for each program.',
  offTitle: 'AI access is off',
  offSealing: 'This PC cannot protect the approval store for your Windows account, so no AI program can connect. Nothing here needs your attention.',
  offStore: 'The approval store could not be opened, so no AI program can connect. Restart Unravis. If this stays, send the support log.',

  // Global stop
  stopButton: 'Stop all AI access',
  stopTitle: 'Every AI program is refused at once, and any call that is running is cancelled. Approvals are kept.',
  stopping: 'Stopping…',
  stoppedTitle: 'AI access is stopped',
  stoppedBody: 'Every AI program is refused. Approvals are kept. Resume to let the approved ones work again.',
  resumeButton: 'Resume AI access',
  resuming: 'Resuming…',
  stopNotSaved: 'AI access is stopped now, but the stop could not be saved. If Unravis restarts, it would be lifted. Stop it again after the restart, or revoke the hosts.',
  stopFailed: 'The stop could not be applied. Revoke the hosts one by one, or close Unravis.',
  resumeFailed: 'AI access could not be resumed. It stays stopped.',

  // Sections
  waitingTitle: 'Needs your decision',
  waitingNone: 'No program needs your approval right now.',
  approvedTitle: 'Approved programs',
  approvedNone: 'No program is approved. None can read your data.',
  endedTitle: 'Ended',

  // A waiting card
  reportedLabel: 'Reported by the program itself. Unravis cannot check it.',
  nameLabel: 'Name',
  versionLabel: 'Version',
  pathLabel: 'Program file',
  pathNote: 'Check that this is the program you expect.',
  idLabel: 'Entry',
  firstSeen: 'First seen',
  notConnectedTitle: 'No program has connected under this name yet.',
  notConnectedBody: 'You can approve it after the program connects. Start the AI program and ask it to use Unravis.',
  contestedTitle: 'Another program also tried to register as this host',
  contestedBody: (n: number) => (n === 1 ? 'One other program tried to use this entry. Only the first one is shown here.' : n + ' other tries to use this entry were made. Only the first one is shown here.'),
  contestedAdvice: 'If you do not know the other program, choose Start over. The old entry is dropped and the real program registers again.',
  contestedAccept: 'I know about the other program and still want to approve this one',
  identifiersAsked: 'This program asked for user ids and addresses. They stay off unless you turn them on below.',

  // The choices
  tierLegend: 'What it may read',
  tierAggregate: 'Summaries only',
  tierAggregateNote: 'Counts, timings and groups of requests. Small groups are hidden.',
  tierEvidence: 'Summaries and request rows',
  tierEvidenceNote: 'Also single requests and the slowest ones.',
  toolsLegend: 'Tools it may use',
  sourcesLegend: 'Sources it may read',
  sourcesNone: 'No source is connected, so there is nothing to approve yet. Connect a source first.',
  identifiersLegend: 'User ids and addresses',
  identifiersLabel: 'Also allow user ids and addresses',
  identifiersNote: 'Off means the program sees hidden-1, hidden-2 and so on in place of them.',
  identifiersForLabel: 'Allow them for',
  codeLegend: 'Code mode',
  codeLabel: 'Allow code mode',
  codeNote: 'Lets the program write and run small scripts over the same tools. Off by default. Leave it off unless you need scripts.',
  idleLegend: 'Stop trusting it after no use for',
  idleNote: 'It must be renewed here after that.',
  hostedWarning: 'The program may send what it reads to a hosted model. Unravis cannot take results back after they are sent.',

  approve: 'Approve',
  approving: 'Approving…',
  deny: 'Deny',
  denying: 'Denying…',
  startOver: 'Start over',
  startingOver: 'Starting over…',
  removeEntry: 'Remove entry',
  removing: 'Removing…',
  whyNot: 'Approve is not ready',
  notWaiting: 'This entry is no longer waiting. Refresh the list.',
  needConnected: 'The program has not connected yet.',
  needTools: 'Pick at least one tool.',
  needSources: 'Pick at least one source.',
  needAccept: 'Tick the box about the other program, or choose Start over.',

  // An approved row
  scopeLabel: 'May read',
  lastCallLabel: 'Last call',
  neverCalled: 'not yet',
  expiresLabel: 'Expires',
  expiredNote: 'It was not used for a while and expired. Renew it to let it work again.',
  notCollectedNote: 'Approved. The program has not collected its access yet. It does so on its next call.',
  codeOnLine: 'Code mode is allowed for this program.',
  identifiersUntilLabel: 'User ids and addresses until',
  toolsLabel: 'Tools',
  sourcesLabel: 'Sources',
  details: 'Details',
  revoke: 'Revoke',
  revoking: 'Revoking…',
  renew: 'Renew',
  renewing: 'Renewing…',
  reissue: 'Start over',
  reissuing: 'Starting over…',
  revokeHelp: 'The next call is refused and a running call is cancelled.',

  // Ended rows
  allowAgain: 'Allow again',
  allowAgainHelp: 'The program must connect again and you approve it again.',
  endedRevoked: 'Revoked. It cannot read anything.',
  endedDenied: 'Denied. It cannot read anything.',

  // After an action
  staleNote: 'This entry changed while you were looking. The list was read again. Look at it once more.',
  actionFailed: 'That did not work.',
  doneApproved: 'Approved. The program gets its access on its next call.',
  doneRevoked: 'Revoked.',
  doneDenied: 'Denied.',
  doneRenewed: 'Renewed.',
  doneReissued: 'Started over. The program must connect again.',
  doneRemoved: 'Removed.'
} as const;

export const TOOL_LABELS: Readonly<Record<string, string>> = {
  audit_get_guide: 'Read the guide for AI programs',
  audit_list_sources: 'List the sources',
  audit_compare: 'Compare two periods',
  audit_get_groups: 'Page through the compared groups',
  audit_get_timeline: 'Read the timeline of one group',
  audit_compare_timelines: 'Compare timelines of groups',
  audit_get_breakdown: 'Break requests down by status, user or version',
  audit_get_requests: 'List single requests',
  audit_get_slowest: 'List the slowest requests',
  audit_get_activity: 'List activity around a request'
};

/** A tool's plain label; an id the screen does not know is shown as it is. */
export const toolLabel = (id: string): string => TOOL_LABELS[id] ?? id;

/** Every fixed string, for the copy lint test. */
export function mcpLintEntries(): LintEntry[] {
  const out: LintEntry[] = [];
  for (const [key, value] of Object.entries(MCP_TEXT)) {
    out.push({ id: 'mcp.' + key, text: typeof value === 'function' ? value(1) : value });
    if (typeof value === 'function') out.push({ id: 'mcp.' + key + '.many', text: value(3) });
  }
  for (const [id, text] of Object.entries(TOOL_LABELS)) out.push({ id: 'mcp.tool.' + id, text });
  return out;
}

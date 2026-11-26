import type { LintEntry } from './copylint';

/**
 * Every fixed word of the code-mode settings popup and the header badge (prototype: web/prototype/code-mode-settings.html, approved
 * 2026-11-26). The words are for a first-time investigator: what code mode is, what each choice allows, and what turning it off does.
 * Nothing here repeats a server message; the server's own words come through `message` of a refused save.
 */

/** The phrase a person types before "no time limit" can be chosen. The page compares it ignoring case and outer spaces. */
export const NO_LIMIT_PHRASE = 'no time limit';

export const CODE_TEXT = {
  title: 'Code mode',
  closeLabel: 'Close code mode',
  stepsLabel: 'Steps',
  steps: ['What it is', 'Switch on', 'Assistants', 'Safety choices', 'Command line', 'Review and confirm', 'Result'] as readonly string[],
  stepNo: (n: number, name: string) => 'Step ' + n + ' of 6 · ' + name,
  resultNo: 'Result',
  skipped: 'Skipped',
  back: 'Back',
  next: 'Next',
  nextReview: 'Next: review',
  done: 'Done',
  change: 'Change',
  changeLabel: (what: string) => 'Change ' + what,
  saving: 'Saving...',

  /* ---- step 1 ---- */
  s0Title: 'What code mode is',
  s0Lede: 'Code mode lets a short script look through your audit data for you. You, or an assistant, can write a few lines that ask many questions at once and put the answers together.',
  /** The three facts of step 1. The last two follow the choices that really apply: what this run of Unravis allows, and the weaker runner. */
  facts: (allowIdentifiers: boolean, allowDegraded: boolean): readonly { icon: string; title: string; body: string }[] => [
    { icon: 'box', title: 'Where it runs', body: 'In a safe runner: a small program that is closed off from the rest of this PC.' },
    {
      icon: 'eye',
      title: 'What it can read',
      body: 'The audit data of the open case, through the read-only tools the app already has. ' +
        (allowIdentifiers
          ? 'Real customer and user IDs are swapped for stand-ins, unless a script asks for the real ones. This run of Unravis was started so that it may.'
          : 'Real customer and user IDs are swapped for stand-ins.')
    },
    {
      icon: 'lock',
      title: 'What it cannot do',
      body: allowDegraded
        ? 'Change your data or open your files. It cannot reach the internet either, except when the full safe runner cannot start: you allowed a weaker one that does not block the network.'
        : 'Change your data, reach the internet or open your files.'
    }
  ],
  runnerTitle: 'The safe runner',
  runnerBody: 'A script runs only if the safe runner is installed on this PC. To check, type unravis code doctor in a terminal. If it is missing, a run is refused and says so.',
  fineOff: 'Code mode is off until you switch it on. Nothing changes until you press the button on the last step.',
  damagedTitle: 'The code-mode settings file could not be trusted, so code mode is off.',
  damagedBody: 'Finish this guide, even if you keep code mode off, and a fresh file is written.',

  /* ---- step 2 ---- */
  s1Title: 'Do you want to switch it on?',
  s1Lede: 'You can turn it off again at any time, from this window or from the badge at the top of the app.',
  offTitle: 'Keep code mode off',
  offBody: 'No script can run, from the app, the command line or an assistant.',
  onTitle: 'Switch code mode on',
  onBody: 'Scripts can run in the safe runner. In the next steps you look at who may use it and how.',
  radioLabel: 'Code mode',
  skipNote: 'Steps 3 to 5 are skipped while it stays off.',

  /* ---- step 3 ---- */
  s2Title: 'Which assistants may run scripts?',
  s2Lede: 'An assistant you connected can run scripts only if you allowed it when you approved it. Its scripts can only read, and only what its own connection already lets it see.',
  s2Loading: 'Looking up your assistants...',
  s2None: 'No assistant is connected. Only you can run scripts.',
  s2Unreadable: 'Your assistants could not be listed just now. This does not change what is saved.',
  s2Allowed: 'May run scripts',
  s2NotAllowed: 'May not run scripts',
  s2Gone: 'Access removed',
  s2Waiting: 'Not approved yet',
  s2Fine: 'To change who may run scripts, open AI access. An assistant gets this right when you approve it there. Nothing on this page changes it.',
  s2Open: 'Open AI access',
  s2Last: 'last used',
  s2Never: 'never used',

  /* ---- step 4 ---- */
  s3Title: 'Safety choices',
  s3Lede: 'The defaults are the careful ones. Change one only if you have a reason.',
  keepTitle: 'Keep a copy of every result',
  keepBody: 'Off: the case keeps only a fingerprint of what each script got back. On: it also keeps a compressed copy, so a run can be looked at again later. It uses more disk.',
  degTitle: 'Allow the weaker safe runner',
  degCareful: 'Careful: ',
  degBody: 'If the full safe runner cannot start, run in a weaker one that does not block the network. Leave this off unless a run tells you it needs it.',
  idsTitle: 'Real IDs in results',
  idsMasked: 'Masked',
  idsAllowed: 'Allowed',
  idsBodyMasked: 'Scripts only ever see stand-ins for IDs, never the real ones. This is set when Unravis is started, so it cannot be changed here.',
  idsBodyAllowed: 'This run of Unravis was started so that a script may ask for real IDs. This is set when Unravis is started, so it cannot be changed here.',
  switchOn: 'On',
  switchOff: 'Off',

  /* ---- step 5 ---- */
  s4Title: 'The command line',
  s4Lede: 'The command unravis code run runs a script from a terminal. It stays blocked unless you open a window here. Opening one is how you say that this is you at the keyboard.',
  cliGroup: 'Command line window',
  keepAsIs: 'Nothing changes for the command line.',
  keepAsIsTitle: (state: string) => 'Leave it as it is: ' + state,
  shutTitle: (recommended: boolean) => 'Keep it shut' + (recommended ? ' (recommended)' : ''),
  shutBody: 'The command line cannot run scripts. Scripts still run in the app and through the assistants you allowed.',
  timedTitle: 'Open it for a set time',
  timedBody: 'It shuts by itself when the time is up.',
  minutesGroup: 'Minutes',
  minutesOr: 'or',
  minutesUnit: 'minutes',
  minutesHint: 'A whole number from 1 to 60. The window is kept in memory only: it shuts by itself, when you switch code mode off, and every time Unravis starts.',
  minutesBad: 'Type a whole number from 1 to 60.',
  minutesTooBig: 'The longest window is 60 minutes. Use a number from 1 to 60.',
  zeroNote: '0 means shut, so the command line stays shut.',
  chip: (m: number) => (m === 60 ? '1 hour' : m + ' min'),
  noLimitTitle: 'No time limit',
  noLimitBody: 'Danger: it stays open until you close it.',
  phraseNotTyped: 'Type no time limit in the box on the command line step first. It is how you confirm this choice.',
  noLimitSoon: 'This choice needs the next update of Unravis. It is not available in this version.',
  dangerTitle: 'Danger: a window with no time limit',
  dangerBody: 'A program that runs as your Windows user can reach the Unravis command line. While the window is open, it can run scripts on your audit data. Unravis cannot tell it from you at the keyboard.',
  dangerPoints: [
    'There is no countdown. It stays open until you close it.',
    'It is not remembered: when Unravis restarts, the command line is shut again.',
    'A timed window gives a program minutes, not hours or days.'
  ] as readonly string[],
  typedLabel: 'To go on, type',
  typedHintNo: 'The Next button stays off until you type it.',
  typedHintYes: 'Confirmed. You can go on, and change your mind on the last step.',

  /* ---- step 6 ---- */
  s5Title: 'Review and confirm',
  s5Lede: 'Nothing has changed yet. Look over your choices, then press the button.',
  rowCode: 'Code mode',
  rowAssistants: 'Assistants',
  rowKeep: 'Copy of every result',
  rowDeg: 'Weaker safe runner',
  rowCli: 'Command line',
  on: 'On',
  off: 'Off',
  offSub: 'No script can run anywhere.',
  kept: 'Kept',
  notKept: 'Not kept',
  allowed: 'Allowed',
  notAllowed: 'Not allowed',
  assistantsSub: 'Set in AI access. This page does not change it.',
  cliShut: 'Shut',
  cliShutSub: 'Scripts from a terminal are refused.',
  cliOffSub: 'Code mode is off.',
  cliStays: 'Stays as it is.',
  cliNoLimit: 'No time limit',
  cliNoLimitSub: 'Open until you close it or Unravis restarts.',
  cliOpenFor: (n: number) => 'Open for ' + n + (n === 1 ? ' minute' : ' minutes'),
  cliOpenForSub: 'Starts when you press the button.',
  cliOpenLeft: (left: string) => 'Open, ' + left + ' left',
  reviewNoLimitTitle: 'You chose no time limit.',
  reviewNoLimitBody: 'The badge at the top of the app will show it until you close the window.',
  confirmOn: 'Turn on code mode',
  confirmSave: 'Save changes',
  confirmOff: 'Turn off code mode',
  confirmKeepOff: 'Finish, keep it off',

  /* ---- step 7 ---- */
  offResultTitle: 'Code mode is off',
  offResultLede: 'No script can run, from the app, the command line or an assistant.',
  offDamagedTitle: 'The settings file could not be trusted.',
  offDamagedBody: 'Press Set it up and finish the guide to write a fresh one, even if you keep it off.',
  setUp: 'Set it up',
  onResultTitle: 'Code mode is on',
  stateOnly: 'Only you can run scripts',
  stateAssistants: (n: number) => n + (n === 1 ? ' assistant may run scripts' : ' assistants may run scripts'),
  cliHeading: 'Command line',
  cliShutLede: 'The command line is shut. unravis code run is refused.',
  openIt: 'Open it for a while',
  openUntil: (clock: string) => 'The command line can run scripts until ' + clock + '.',
  closeNow: 'Close the command line now',
  changeWindow: 'Change the window',
  fifteen: 'Switch to a 15 minute window',
  noLimitNowTitle: 'Danger: the command line has no time limit',
  noLimitNowBody: 'A program that runs as your Windows user can reach the command line. Right now it can run scripts. This stays so until you close it or Unravis restarts.',
  offHeading: 'How to turn it off',
  offHow: 'Press Turn off code mode below, or click the Code mode badge at the top of the app and press it there. Everything stops at once, including the command line.',
  turnOff: 'Turn off code mode',
  changeSettings: 'Change settings',
  minutesLeft: (n: number) => n + (n === 1 ? ' minute left' : ' minutes left'),

  /* ---- messages after a save or a refresh ---- */
  savedOn: 'Saved. Code mode is on.',
  savedOff: 'Saved. Code mode is off.',
  closedCli: 'Closed. The command line is blocked again.',
  fifteenDone: 'Now a 15 minute window.',
  turnedOff: 'Code mode is off. Everything has stopped.',
  windowEnded: 'The window closed. The command line is blocked again.',
  notApplied: 'Unravis answered, but did not apply what you asked for. Look at the result below.',
  notAppliedNoLimit: 'Unravis answered, but the command line is still open with no time limit. Press Close the command line now.',
  unreachable: 'Unravis could not be reached. Check that it is still running.',
  unreadable: 'Unravis sent an answer that could not be read.',
  refusedBase: 'Unravis did not save this.',
  unavailableTitle: 'Code mode is not available in this run of Unravis',
  unavailableBody: 'This run of Unravis has no code mode, so there is nothing to set up here.',
  refreshFailed: 'The latest state could not be read. What you see may be out of date.',
  loadingTitle: 'Reading the code-mode settings...',
  retry: 'Try again',

  /* ---- the badge ---- */
  badgeOn: 'Code mode on',
  badgeOff: 'Code mode off',
  badgeShut: 'command line shut',
  badgeNoLimit: 'no time limit',
  badgeDamaged: 'settings file damaged',
  badgeLeft: (clock: string) => clock + ' left',
  badgeShortOn: 'On',
  badgeShortOff: 'Off',
  badgeShortNoLimit: 'No limit',
  // The accessible name starts with the words the badge shows ("Code mode on ·"), then says the state in full.
  badgeAriaShut: 'Code mode on · command line shut. Scripts from a terminal are refused. Open code mode settings.',
  badgeAriaNoLimit: 'Code mode on · no time limit. The command line stays open until you close it. Open code mode settings.',
  badgeAriaDamaged: 'Code mode off · settings file damaged. Open code mode settings.',
  badgeAriaOpen: (minutes: number) => 'Code mode on · ' + (minutes === 1 ? 'about 1 minute left' : 'about ' + minutes + ' minutes left') + '. The command line is open. Open code mode settings.',

  /* ---- the entry in AI access ---- */
  entryTitle: 'Code mode',
  entryBody: 'Let a short script look through your audit data. It is off until you switch it on.',
  entryOpen: 'Open code mode',
  entryOnBody: 'On. Open it to see the state or turn it off.',
  entryDamaged: 'Off. Its settings file could not be trusted.'
} as const;

/** Every fixed string, for the copy lint test. Functions are tried with a small and a large number. */
export function codeModeLintEntries(): LintEntry[] {
  const out: LintEntry[] = [];
  for (const [key, value] of Object.entries(CODE_TEXT)) {
    const id = 'code.' + key;
    if (key === 'facts') {
      // Every wording of the step 1 facts: the plain one, and the ones that follow what is allowed.
      [[false, false], [true, false], [false, true]].forEach(([ids, deg], n) => (value as typeof CODE_TEXT.facts)(ids, deg).forEach((v, i) => out.push({ id: id + '.' + n + '.' + i, text: v.title + ' ' + v.body })));
      continue;
    }
    if (typeof value === 'string') out.push({ id, text: value });
    else if (typeof value === 'function') {
      const fn = value as (...a: never[]) => string;
      const args: unknown[][] = key === 'stepNo' ? [[2, 'Switch on']] : key === 'changeLabel' || key === 'keepAsIsTitle' || key === 'badgeLeft' || key === 'cliOpenLeft' || key === 'openUntil' ? [['x']] : key === 'shutTitle' ? [[true], [false]] : [[1], [3]];
      args.forEach((a, i) => out.push({ id: id + '.' + i, text: (fn as (...x: unknown[]) => string)(...a) }));
    } else if (Array.isArray(value)) {
      value.forEach((v, i) => out.push({ id: id + '.' + i, text: typeof v === 'string' ? v : Object.values(v as Record<string, string>).filter((x) => x !== (v as { icon?: string }).icon).join(' ') }));
    }
  }
  return out;
}

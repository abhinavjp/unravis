const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const root = path.join(__dirname, '..', 'src');
const wrapper = fs.readFileSync(path.join(root, 'components', 'BlackboxPlaceWizard.svelte'), 'utf8');
const stepsDir = path.join(root, 'components', 'blackboxWizard');
const stepFiles = fs.readdirSync(stepsDir).filter((name) => name.endsWith('.svelte'));

test('the pop-up runs the real install and pack calls and saves the pack', () => {
  assert.match(wrapper, /blackboxApi\.install\(/);
  assert.match(wrapper, /blackboxApi\.pack\(/);
  assert.match(wrapper, /blackboxApi\.validate\(/);
  assert.match(wrapper, /saveBlob\(made\.blob, made\.fileName\)/);
  assert.match(wrapper, /refusalText\(error, BB_REFUSAL\)/);
});

test('the payload sent is buildSettings and nothing else', () => {
  assert.match(wrapper, /const body = \$state\.snapshot\(buildSettings\(wizard\)\) as Settings;/);
  assert.match(wrapper, /blackboxApi\.install\(body\)/);
  assert.match(wrapper, /blackboxApi\.pack\(body\)/);
  assert.match(wrapper, /blackboxApi\.validate\(\$state\.snapshot\(buildSettings\(wizard\)\)/);
});

test('SQL Server on this PC is read from the targets list', () => {
  assert.match(wrapper, /blackboxApi\.targets\(/);
  assert.match(wrapper, /MSSQLSERVER/);
  assert.match(wrapper, /setSqlSeen\(/);
});

test('the dialog gets every label and the wizard state travels as wizard, never as state', () => {
  for (const key of ['close', 'leaveTitle', 'leaveBody', 'keepGoing', 'leave', 'busyNote']) {
    assert.match(wrapper, new RegExp('\\b' + key + ':\\s*W\\['));
  }
  assert.match(wrapper, /\{labels\}/);
  assert.doesNotMatch(wrapper, /\bstate=\{/);
  assert.doesNotMatch(wrapper, /\bbind:state\b/);
  for (const file of stepFiles) {
    const source = fs.readFileSync(path.join(stepsDir, file), 'utf8');
    assert.doesNotMatch(source, /\bstate\s*[:=]\s*\$bindable/, file);
    assert.doesNotMatch(source, /MISSING_COPY/, file);
  }
});

test('every refused step maps to a prop the step reads', () => {
  assert.match(wrapper, /<StepName [^>]*showProblem=\{attempted\}/);
  assert.match(wrapper, /<StepFolder [^>]*\{attempted\}/);
  assert.match(wrapper, /<StepWatch [^>]*problem=\{watchProblem\}/);
  assert.match(wrapper, /where-missing/);
  assert.match(wrapper, /startsWith\('targets-'\)/);
});

test('Review is checked on every way in, and the leave copy does not promise kept answers', () => {
  assert.match(wrapper, /if \(to\.step === 'review'\) void enterReview\(\)/);
  const copy = fs.readFileSync(path.join(root, 'lib', 'copy', 'blackboxWizardCopy.ts'), 'utf8');
  assert.doesNotMatch(copy, /answers stay until/i);
});

test('leaving after a lost-contact PC install reloads the list, because the copy says to check it', () => {
  assert.match(wrapper, /refusal\.reason === 'offline'/);
  assert.match(wrapper, /else if \(phase\.kind === 'pack' \|\| partial \|\| lostContact\) onPlaced\(null\);/);
});

test('the Windows drive letter in the Folder step comes from the wizard, not from a hardcoded C:', () => {
  const folder = fs.readFileSync(path.join(stepsDir, 'StepFolder.svelte'), 'utf8');
  for (const key of ['notice.systemDrive', 'folder.confirmC', 'folder.confirmCError', 'folder.noOtherDrive']) {
    assert.ok(folder.includes("W['" + key + "'](systemLetter)"), key);
  }
});

test('the pop-up uses colour tokens only', () => {
  const style = wrapper.slice(wrapper.indexOf('<style>'));
  assert.doesNotMatch(style, /#[0-9a-fA-F]{3,8}\b/);
  assert.doesNotMatch(style, /\brgba?\(|\bhsla?\(/);
});

// Browser pass, defects A to J: the wiring that the pure-function tests cannot see.
const stepSource = (name) => fs.readFileSync(path.join(stepsDir, name), 'utf8');
const dialogSource = fs.readFileSync(path.join(stepsDir, 'WizardDialog.svelte'), 'utf8');

test('A: on Review Enter acts only through the button, and a held-down Enter is ignored (defect A)', () => {
  assert.match(wrapper, /const enterNeedsButton = \$derived\(wizard\.step === 'review' && phase\.kind === 'idle'\)/);
  assert.match(wrapper, /\{enterNeedsButton\}/);
  assert.match(wrapper, /function primaryKey\(e: KeyboardEvent\)[^}]*e\.key === 'Enter' && e\.repeat[^}]*preventDefault\(\)/);
  assert.match(wrapper, /onkeydown=\{primaryKey\}/);
  assert.match(dialogSource, /enterNeedsButton = false/);
  assert.match(dialogSource, /enterDoesPrimary\(event\.target as HTMLElement \| null, enterNeedsButton\)/);
  assert.match(dialogSource, /event\.repeat/);
});

test('B: the rail leaves room for the close button so the x never covers the last dot (defect B)', () => {
  // Not visually verified (no build allowed in this pass): the rule is that the rail's right padding clears the 40px close button, also at phone width.
  // Desktop follows the wireframe (24px each side; the last label ends before the 40px x). Phone width hides the other labels, so only it keeps the extra room.
  const rails = dialogSource.match(/\.shell-rail \{[^}]*\}/g) || [];
  assert.ok(rails.length >= 2, 'a desktop and a phone-width rule');
  assert.match(rails[rails.length - 1], /padding:[^;]*calc\(var\(--space-\d\) \+ 40px \+ var\(--space-\d\)\)/, rails[rails.length - 1]);
});

test('D: a refused Next opens a closed Advanced section before it focuses the field (defect D)', () => {
  assert.match(wrapper, /field\.closest\('details'\)/);
  assert.match(wrapper, /closed\.open = true;\s*await tick\(\);\s*\}\s*field\.focus\(\)/);
  const folder = stepSource('StepFolder.svelte');
  assert.match(folder, /void attempted;\s*if \(problem === 'port' \|\| problem === 'bind'\) advancedOpen = true;/);
  assert.match(folder, /FOLDER_WORDS/);
  assert.match(folder, /W\['folder\.errPort'\]/);
});

test('F: Where shows the scan notice and the helper-missing line, and Next stays off with a reason (defect F)', () => {
  const where = stepSource('StepWhere.svelte');
  assert.match(where, /choices\.scanFailed/);
  assert.match(where, /choices\.none/);
  assert.match(where, /where\.noneHelper/);
  assert.match(where, /where\.noneOther/);
  assert.match(wrapper, /whereChoices\(thisPc, canPack\)\.none !== null/);
  assert.match(wrapper, /busy \|\| checking \|\| whereBlocked/);
  assert.match(wrapper, /W\['where\.nextOff'\]/);
  assert.match(wrapper, /aria-describedby=\{whereBlocked \? nextReasonId : undefined\}/);
});

test('G: Name shows the no-spaces and the 63-character notes (defect G)', () => {
  const name = stepSource('StepName.svelte');
  assert.match(name, /BBW_TEXT\['name\.space'\]/);
  assert.match(name, /BBW_TEXT\['name\.limit'\]/);
  assert.match(name, /atLimit/);
});

test('H: every step that starts with a text field marks it, and a step change focuses it or else the heading (defect H)', () => {
  for (const file of ['StepName.svelte', 'StepKeep.svelte', 'StepFolder.svelte']) {
    assert.match(stepSource(file), /\bdata-autofocus\b/, file);
  }
  assert.match(wrapper, /dialog\?\.focusStep\(\)/);
  assert.doesNotMatch(wrapper, /focusHeading/);
  assert.doesNotMatch(dialogSource, /focusHeading/);
  assert.match(dialogSource, /export function focusStep\(\): void/);
  assert.match(dialogSource, /\.shell-body \[data-autofocus\]:not\(\[disabled\]\)/);
  assert.match(dialogSource, /aria-live/, 'the step is still announced to a screen reader');
});

test('I: the "use defaults, go to review" link shows on Record, SQL and Watch only, as in the wireframe, and still resets the keep numbers when asked on Keep (defect I)', () => {
  assert.match(wrapper, /wizard\.step === 'record' \|\| wizard\.step === 'sql' \|\| wizard\.step === 'watch'\)/);
  assert.match(wrapper, /useDefaultsToReview\(wizard, wizard\.step === 'keep'\)/);
});

test('J: the SQL and Folder leads say "the server" on the pack path (defect J)', () => {
  assert.match(wrapper, /sql: wizard\.where === 'pack' \? W\['step\.sql\.leadPack'\] : W\['step\.sql\.lead'\]/);
  assert.match(wrapper, /folder: wizard\.where === 'pack' \? W\['step\.folder\.leadPack'\] : W\['step\.folder\.lead'\]/);
  assert.match(stepSource('StepReview.svelte'), /W\['review\.packWill'\]\(fileName\)/);
});

# Packet F1: two small apply scripts (`buildout/packets/data/apply-help-fixes.js`, `buildout/packets/data/apply-line-fixes.js`)

Do exactly this. Do not explore the repo, do not read other files except the two JSON inputs named below. Create exactly two new files. Node, plain CommonJS, ES5 style (`var`, `function`). Do NOT run them on the real files; only run the check below. Paths in the JSON are relative to `buildout` (the folder two levels above `packets/data`); resolve them with `path.join(__dirname, '..', '..', file)`.

## 1. `apply-help-fixes.js`  (input: `help-fixes.json` next to it, an array of `{id, field, value}`; `value` is a string, or for `field` `terms` an array of `{t,d}`)
- The help files are `buildout/wireframes/parts/14-help-sql1.js`, `14-help-sql2.js`, `14-help-mongo1.js`, `14-help-mongo2.js`, `14-help-host1.js`, `14-help-host2.js`. An optional first command-line argument is a directory that replaces `buildout/wireframes/parts` (used by the check).
- Each file's first line is `var EVHELP = (typeof EVHELP === 'object' && EVHELP) || {};` and every following non-empty line has the form `EVHELP["<id>"] = { ... };` (one card per line). Keep line endings: detect `\r\n` in the file and use the same when joining; keep a trailing line ending if the file had one.
- For each distinct `id` in the fixes: find the file and line whose text starts with `EVHELP["<id>"] = `. Parse the object by evaluating the text after ` = ` (strip the final `;`) with `vm.runInNewContext('(' + text + ')')`. Apply every fix for that id (`obj[field] = value`). If the id is not found in any file, or a field is not one of `look,what,normal,issue,check,chart,terms`, print `FAIL <reason>` and exit with code 1 without writing anything.
- Rewrite the line as `EVHELP["<id>"] = { look: <J>, what: <J>, normal: <J>, issue: <J>, check: <J>, chart: <J>, terms: [{ t: <J>, d: <J> }, ...] };` where `<J>` is `JSON.stringify(string)`, keys in exactly that order, terms joined by `, `. Write all files back only after every id succeeded. Print `OK <number of cards changed> cards` and exit 0.

## 2. `apply-line-fixes.js`  (input: `line-fixes.json` next to it, an array of `{file, from, to, after?, all?}`)
- Process the entries in order. Read the file as a UTF-8 string (do not touch its line endings). If `all` is true, replace every occurrence of `from` with `to`. Otherwise: `start` = index of the first occurrence of `after` (use 0 when `after` is missing); `pos` = index of the first occurrence of `from` at or after `start`; replace just that one occurrence. If a file cannot be read, or `after` or `from` is not found, or an `all` entry matches nothing, print `FAIL <file> <from>` and exit with code 1 without writing any file. After all entries succeed write every changed file and print `OK <number of entries> edits` and exit 0. An optional first command-line argument is a directory that replaces `buildout` as the base for `file`.

## Check (run from the repo root; it must print `PASS`)
```
node --check buildout/packets/data/apply-help-fixes.js && node --check buildout/packets/data/apply-line-fixes.js && node -e "
var fs=require('fs'),os=require('os'),path=require('path'),cp=require('child_process');
var d=fs.mkdtempSync(path.join(os.tmpdir(),'fx'));
var head='var EVHELP = (typeof EVHELP === \'object\' && EVHELP) || {};';
var card='EVHELP[\"sql-waits-benign\"] = { look: \"L\", what: \"W\", normal: \"N\", issue: \"I\", check: \"C\", chart: \"H\", terms: [{ t: \"a\", d: \"b\" }] };';
['sql1','sql2','mongo1','mongo2','host1','host2'].forEach(function(n){fs.writeFileSync(path.join(d,'14-help-'+n+'.js'),n==='sql1'?head+'\r\n'+card+'\r\n':head+'\r\n');});
fs.writeFileSync(path.join(d,'fx.json'),'[]');
var t=path.join(d,'t'); fs.mkdirSync(path.join(t,'x'),{recursive:true}); fs.writeFileSync(path.join(t,'x','a.txt'),'k \"1\" k \"1\" id \"1\" \"1\"');
var rs=[];
var r1=cp.spawnSync('node',['buildout/packets/data/apply-help-fixes.js',d]);rs.push(r1.status===1&&/FAIL/.test(String(r1.stdout)));
var r2=cp.spawnSync('node',['buildout/packets/data/apply-line-fixes.js',t]);rs.push(r2.status===1&&/FAIL/.test(String(r2.stdout)));
console.log(rs.every(Boolean)?'PASS':'FAIL '+rs.join()+' '+r1.stdout+r1.stderr+r2.stdout+r2.stderr);
"
```
(The check only proves both scripts load and fail cleanly on unknown ids or entries; the real inputs are applied by the owner afterwards. The check runs against the real JSON inputs, which do not match the temp data, so exit code 1 with `FAIL` is the expected result.)

## Output
Both files in separate code blocks headed `### buildout/packets/data/apply-help-fixes.js` and `### buildout/packets/data/apply-line-fixes.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.

// Remote hub helper. Never prints the token. Usage:
//   node hub.mjs get <path>                      -> prints body
//   node hub.mjs upload <file>                   -> prints upload id
//   node hub.mjs submit <lane> <repoId> <base> <timeoutSec> <owner> <remoteId> <promptFile>
//   node hub.mjs status <jobId>
//   node hub.mjs patch <jobId> <outFile>
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
const env = Object.fromEntries(fs.readFileSync(path.join(os.homedir(), '.fjsecrets', 'cloud_hub.env'), 'utf8').split(/\r?\n/).filter((l) => l.includes('=')).map((l) => [l.slice(0, l.indexOf('=')).trim(), l.slice(l.indexOf('=') + 1).trim().replace(/^["']|["']$/g, '')]));
const base = env.CLOUD_HUB_URL.replace(/\/$/, '');
const headers = { Authorization: 'Bearer ' + env.CLOUD_HUB_TOKEN, 'User-Agent': 'unravis-delegator/1.0 (+local)' };
async function call(method, p, body, extra = {}) {
  const res = await fetch(base + p, { method, headers: { ...headers, ...extra }, body });
  const text = await res.text();
  if (!res.ok) { console.error('HTTP ' + res.status + ' ' + text.slice(0, 400)); process.exit(1); }
  return text;
}
const [cmd, ...a] = process.argv.slice(2);
if (cmd === 'get') console.log(await call('GET', a[0]));
else if (cmd === 'upload') console.log(await call('POST', '/api/uploads', fs.readFileSync(a[0]), { 'Content-Type': 'application/octet-stream' }));
else if (cmd === 'submit') {
  const [lane, repoId, baseRef, timeout, owner, remoteId, promptFile] = a;
  const body = JSON.stringify({ lane, prompt: fs.readFileSync(promptFile, 'utf8'), repo: 'upload:' + repoId, base: baseRef, timeout: Number(timeout), owner, remote_id: remoteId });
  console.log(await call('POST', '/api/jobs', body, { 'Content-Type': 'application/json' }));
} else if (cmd === 'status') console.log(await call('GET', '/api/jobs/' + a[0]));
else if (cmd === 'patch') { fs.writeFileSync(a[1], await call('GET', '/api/jobs/' + a[0] + '/patch')); console.log('saved ' + a[1]); }
else { console.error('unknown'); process.exit(2); }

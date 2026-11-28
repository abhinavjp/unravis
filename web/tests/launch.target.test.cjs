const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// A start link from the tray (or a notice's click) carries one screen name in the fragment (#t=TOKEN&open=updates). Only the three
// known names are taken, and only once.

const api = loadTypeScript('src/lib/api.ts');

function openFragment(hash) {
  const store = new Map();
  global.sessionStorage = { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => store.set(k, v), removeItem: (k) => store.delete(k) };
  global.location = { hash, pathname: '/', search: '' };
  global.history = { state: null, replaceState: () => {} };
  return api.readSessionToken();
}

test('a start link names a screen once', () => {
  for (const to of ['updates', 'whatsnew', 'blackbox']) {
    assert.equal(openFragment('#t=TOKEN&open=' + to), 'TOKEN');
    assert.equal(api.takeLaunchTarget(), to);
    assert.equal(api.takeLaunchTarget(), '', 'taken once');
  }
});

test('a link with no screen, or an unknown one, asks for nothing', () => {
  for (const hash of ['#t=TOKEN', '#t=TOKEN&open=', '#t=TOKEN&open=settings', '#t=TOKEN&open=%3Cscript%3E']) {
    assert.equal(openFragment(hash), 'TOKEN');
    assert.equal(api.takeLaunchTarget(), '', hash);
  }
});

test('a screen name without a token is ignored', () => {
  openFragment('#open=updates');
  assert.equal(api.takeLaunchTarget(), '');
});

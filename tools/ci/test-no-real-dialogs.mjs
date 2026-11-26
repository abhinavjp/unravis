// Self-test of tools/ci/no-real-dialogs.mjs.   node tools/ci/test-no-real-dialogs.mjs
import assert from 'node:assert/strict';
import { scanText } from './no-real-dialogs.mjs';

const hit = (src) => scanText('x_test.go', src);
assert.equal(hit('\tgot := AskOKCancel("Unravis test", "x", wait)').length, 1, 'a direct call is flagged');
assert.equal(hit('\tsetup.ShowNotice("t", "x")').length, 1, 'a qualified call is flagged');
assert.equal(hit('\twindows.MessageBox(0, c, t, 0)').length, 1, 'a raw MessageBox is flagged');
assert.equal(hit('\tshowMessageBox("t", "x")').length, 1, 'the fatal box is flagged');
assert.equal(hit('\tAskOKCancel("t", "x", w) // dialogok: askBox is faked').length, 0, 'a marked line passes');
assert.equal(hit('\t// AskOKCancel("t", "x", w)').length, 0, 'a comment passes');
assert.equal(hit('func fakeBox() { }').length, 0, 'a definition passes');
assert.equal(hit('func TestShowNoticeDoes(t *testing.T) {').length, 0, 'a test NAME is not a call');
assert.equal(hit('func (f *fake) ShowNotice(title, text string) {').length, 0, 'a fake method definition passes');
assert.equal(hit('\tshowMessage = showMessageBox').length, 0, 'assigning the function (no call) passes');
assert.equal(hit('\tt.Log("ShowNotice(x) is not called")').length, 0, 'text in a string passes');
console.log('test-no-real-dialogs: ok');

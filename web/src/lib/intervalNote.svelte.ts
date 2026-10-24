/** One short line about the chart interval, shared by the controls (the range chips and the interval field live in different parts of the page). */
let text = $state('');
export const intervalNote = {
  get text() { return text; },
  set(value: string) { text = value; },
  clear() { text = ''; }
};

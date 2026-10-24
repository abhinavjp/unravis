/**
 * The words of the zoom strip. Plain and factual: they say which time window the charts show and how it was read, and never why
 * anything happened. Every string here passes the copy lint (tests/zoom.test.cjs).
 */
export const zoomCopy = {
  heading: 'Zoom',
  expand: 'Zoom into one part of this period',
  levelNames: { window: 'Whole period', day: 'Day', hour: 'Hour', minute: 'Minute' } as const,
  back: 'Back',
  earlier: 'Earlier',
  later: 'Later',
  zoomIn: 'Zoom in',
  zoomOut: 'Zoom out',
  reset: 'Reset',
  breadcrumbLabel: 'Zoom steps',
  overviewLabel: 'Whole period, with the zoomed part marked',
  overviewEmpty: 'No requests in the whole period.',
  howTo: 'Click a bar to zoom in. Drag across a chart to zoom to a range. On a focused chart, arrow keys move, Enter zooms in, and Backspace or Escape steps out.',
  loading: 'Reading this window...',
  noData: 'No requests were found in this window. Go back, or pick a wider one.',
  referenceNote: 'Each zoomed read is compared with the same length of time just before it.',
  automatic: 'automatic',
  manual: 'your setting',
  atTop: 'This is the whole period you chose.',
  tooShort: 'This window is too short to zoom into.',
  cannotRead: 'This window could not be read.',
  minuteFloor: 'This is the smallest window.',
  tapToZoom: 'Press Enter to zoom in.'
} as const;

export const zoomText = {
  collapsed: (label: string) => 'Zoom: ' + label,
  showing: (range: string) => 'Showing ' + range + '.',
  interval: (text: string, source: 'automatic' | 'manual') => 'Interval: ' + text + ' (' + (source === 'manual' ? zoomCopy.manual : zoomCopy.automatic) + ').',
  intervalRaised: (text: string, asked: string) => 'Interval: ' + text + '. Your setting of ' + asked + ' would give more than 120 points here.',
  asOf: (time: string) => 'As of ' + time + '.',
  scope: (range: string) => 'Charts follow this window. The figures and the list below still cover the whole period: ' + range + '.',
  cannotRead: (detail: string) => zoomCopy.cannotRead + (detail ? ' ' + detail : ''),
  slot: (index: number, count: number, range: string) => 'Part ' + index + ' of ' + count + ': ' + range + '.',
  overviewAlt: (range: string, whole: string) => 'Zoomed to ' + range + ' of ' + whole + '.'
};

/** Every fixed string, for the copy lint. */
export const allZoomStrings = (): string[] => [
  ...(Object.values(zoomCopy) as unknown[]).filter((value): value is string => typeof value === 'string'),
  ...Object.values(zoomCopy.levelNames),
  zoomText.collapsed('7 days'), zoomText.showing('20 Oct 00:00 to 21 Oct 00:00 UTC'), zoomText.interval('1 hour', 'automatic'), zoomText.interval('5 minutes', 'manual'),
  zoomText.intervalRaised('1 hour', '10 seconds'), zoomText.asOf('14:03:22 UTC'), zoomText.scope('13 Oct 00:00 to 20 Oct 00:00 UTC'),
  zoomText.cannotRead('The server did not answer.'), zoomText.slot(3, 24, '02:00 to 03:00 UTC'), zoomText.overviewAlt('20 Oct', '7 days')
];

// Origin suspect evidence cards for Step 10 ("Origin suspects").
// Same card object shape as the cards in parts/14-scenarios.js. Wireframe fixtures only.
var ORIGIN_CARDS = [
  {
    id: 'ORI-01-sql-waits',
    label: 'SQL Server waits moved first',
    verdict: 'bad',
    rule: 'Red: the bottleneck wait on SQL Server grew to 31 s per minute at 09:14, the same minute the slow end moved, against 0.4 s per minute in the reference hour. Unit: seconds of wait per minute.',
    chart: 'stackedArea',
    series: [
      { name: 'Lock waits (s/min)', win: 'normal', pts: [ { t: 0, v: 0.4 }, { t: 10, v: 0.5 }, { t: 20, v: 0.4 }, { t: 30, v: 0.6 }, { t: 40, v: 0.4 }, { t: 50, v: 0.5 } ] },
      { name: 'Lock waits (s/min)', win: 'incident', pts: [ { t: 60, v: 4 }, { t: 70, v: 12 }, { t: 80, v: 31 }, { t: 90, v: 20 }, { t: 100, v: 8 }, { t: 110, v: 3 } ] }
    ],
    table: {
      cols: ['Window', 'Lock waits', 'Vs reference'],
      rows: [
        ['08:00-08:59', '0.4 s/min', 'reference'],
        ['09:00-09:59', '31 s/min', '+30.6 s/min']
      ]
    },
    why: {
      summary: 'The SQL Server wait is the first signal in the window and it lines up with the onset, so it is the strongest fit.',
      steps: [
        'Reads the wait deltas for the incident hour and the reference hour.',
        'Lock waits go 0.4 s/min to 31 s/min at 09:14, the same minute the slow end moved.',
        'The size and the timing both fit, so this row is red.'
      ],
      dataNote: 'Wait stats are cumulative and differenced across snapshots taken every 60 s; spikes between samples can be missed.'
    },
    unit: 'seconds of wait per minute',
    sampled: true
  },
  {
    id: 'ORI-02-app-followed',
    label: 'App tier followed within the minute',
    verdict: 'watch',
    rule: 'Amber: Orders.Create P95 rose from 420 ms to 2,100 ms, but only after the SQL wait moved, so the app tier reads as a follower rather than the origin. Unit: milliseconds.',
    chart: 'line',
    series: [
      { name: 'Orders.Create p95 (ms)', win: 'normal', pts: [ { t: 0, v: 420 }, { t: 20, v: 418 }, { t: 40, v: 422 }, { t: 50, v: 420 } ] },
      { name: 'Orders.Create p95 (ms)', win: 'incident', pts: [ { t: 60, v: 460 }, { t: 61, v: 900 }, { t: 70, v: 2100 }, { t: 90, v: 1900 }, { t: 110, v: 1500 } ] }
    ],
    table: {
      cols: ['Window', 'P95', 'Vs reference'],
      rows: [
        ['08:00-08:59', '420 ms', 'reference'],
        ['09:00-09:59', '2,100 ms', '+1,680 ms']
      ]
    },
    why: {
      summary: 'The app tier is slower, but it moves after the SQL wait, so it is a candidate follower, not the first mover.',
      steps: [
        'Reads P95 per API for the incident hour and the reference hour.',
        'Orders.Create goes 420 ms to 2,100 ms, starting after 09:14.',
        'The move lags the SQL wait, so this row is amber.'
      ],
      dataNote: 'P95 is computed from the request log; only API routes in the list are compared.'
    },
    unit: 'milliseconds'
  },
  {
    id: 'ORI-03-mongo-untested',
    label: 'MongoDB suspects could not be tested',
    verdict: 'none',
    rule: 'Gray: not collected. No MongoDB source is connected, so its suspects can be neither confirmed nor ruled out. Unit: not applicable.',
    chart: 'none',
    series: [],
    table: {
      cols: ['Source', 'State'],
      rows: [
        ['MongoDB', 'Missing'],
        ['Host', 'Missing']
      ]
    },
    why: {
      summary: 'Without the MongoDB source there is no data to judge, so the suspect stays open.',
      steps: [
        'Looks for a MongoDB source in the case.',
        'None is connected, so no slow-operation or replica-set data exists.',
        'With no data the row is gray, which is never a pass.'
      ],
      dataNote: 'Gray means not tested. It is not evidence that MongoDB was healthy.'
    },
    unit: 'not applicable'
  }
];

function step10Evidence() {
  var i, card, out;
  if (W.why) {
    card = null;
    for (i = 0; i < ORIGIN_CARDS.length; i++) {
      if (ORIGIN_CARDS[i] && ORIGIN_CARDS[i].id === W.why) { card = ORIGIN_CARDS[i]; break; }
    }
    if (card && typeof whyView === 'function') { return whyView(card); }
    W.why = null;
  }
  out = '';
  if (typeof nutshell === 'function') {
    out += nutshell('Three origin suspects fit the first-move timeline: the SQL Server wait moved first, the app tier followed within the same minute, and the MongoDB suspect could not be tested. Unravis does not name a culprit; it says what fits and what could not be tested.');
  } else {
    out += '<p class="lead" style="color:var(--text)">Three origin suspects fit the first-move timeline: the SQL Server wait moved first, the app tier followed within the same minute, and the MongoDB suspect could not be tested. Unravis does not name a culprit; it says what fits and what could not be tested.</p>';
  }
  out += evidenceLegend();
  out += '<div style="display:grid;gap:12px">';
  for (i = 0; i < ORIGIN_CARDS.length; i++) {
    out += evidenceCard(ORIGIN_CARDS[i]);
  }
  out += '</div>';
  return out;
}

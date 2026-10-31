// Nearby activity (step 4): deterministic fixture rows for the swim-lane and its table.
// One row per recorded event. at = UTC clock time, src = lane (API, SQL, Mongo, Host, IIS, Events),
// what = the table text, t = start minute on the shared chart axis (minute 60 is 09:00 UTC,
// the onset 09:14 is minute 74), dur = length in minutes (0 = a point in time),
// sev = ok, warn or bad. The case has no MongoDB, Host, IIS or Windows events source, so those lanes stay empty.
var NEAR_ROWS = [
  { at:'09:07:10', src:'API', what:'Orders.List call, 450 ms',               t:67, dur:0, sev:'ok' },
  { at:'09:09:44', src:'API', what:'Catalog.Search call, 312 ms',            t:69, dur:0, sev:'ok' },
  { at:'09:11:20', src:'SQL', what:'Log flush held the write batch, 900 ms',  t:71, dur:0, sev:'warn' },
  { at:'09:12:58', src:'API', what:'Basket.Get call, 205 ms',                t:72, dur:0, sev:'ok' },
  { at:'09:13:44', src:'API', what:'Orders.Get call, 388 ms',                t:73, dur:0, sev:'ok' },
  { at:'09:14:02', src:'API', what:'Orders.Create call, 4,812 ms',           t:74, dur:0, sev:'bad' },
  { at:'09:14:01', src:'SQL', what:'Lock wait on Orders (session 88)',      t:74, dur:9, sev:'bad' },
  { at:'09:15:31', src:'API', what:'Payments.Authorize call, 1,240 ms',      t:75, dur:0, sev:'warn' },
  { at:'09:16:40', src:'SQL', what:'Lock wait on OrderLines (session 91)',   t:76, dur:0, sev:'warn' },
  { at:'09:17:12', src:'API', what:'Catalog.Search call, 312 ms',            t:77, dur:0, sev:'ok' },
  { at:'09:18:58', src:'API', what:'Orders.Create call, 3,611 ms',           t:78, dur:0, sev:'bad' },
  { at:'09:21:40', src:'API', what:'Orders.List call, 3,904 ms',             t:81, dur:0, sev:'bad' },
  { at:'09:23:00', src:'SQL', what:'Lock wait on Orders (session 88) ended', t:83, dur:0, sev:'ok' },
  { at:'09:24:11', src:'API', what:'Orders.Create call, 4,812 ms',           t:84, dur:0, sev:'bad' }
];

// Brush width in minutes lives in W.near.min, created on first use.
function nearSet(m){
  if (!W.near || typeof W.near !== 'object') W.near = { min: 5 };
  W.near.min = m;
  render();
}

// The "Nearby activity" block of step 4: one swim-lane of all six lanes on the
// incident hour, a brush of m minutes around the onset, and the events inside it.
function nearView(){
  if (!W.near || typeof W.near !== 'object') W.near = { min: 5 };
  var m = W.near.min === 2 || W.near.min === 15 ? W.near.min : 5;
  var anchor = 74; // 09:14 UTC: the minute the slow end moved
  var bFrom = anchor - Math.floor(m / 2), bTo = anchor + Math.ceil(m / 2);
  var hhmm = function(min){
    var t = 480 + Math.round(min), h = Math.floor(t / 60), mm = t % 60;
    return (h < 10 ? '0' : '') + h + ':' + (mm < 10 ? '0' : '') + mm;
  };
  var lanes = ['API', 'SQL', 'Mongo', 'Host', 'IIS', 'Events'].map(function(src){
    return { name: src, events: NEAR_ROWS.filter(function(r){ return r.src === src; }).map(function(r){
      return { t: r.t, dur: r.dur, label: r.what, sev: r.sev };
    }) };
  });
  var rows = NEAR_ROWS.filter(function(r){ return r.t <= bTo && r.t + (r.dur || 0) >= bFrom; });
  return '<section class="nearby near" aria-label="Nearby activity">'
  + '<div class="sthead"><h4>Nearby activity around the onset (09:14 UTC)</h4></div>'
  + '<p class="note">Every source we have, on one time line: the shaded band is the slice the buttons choose, and the table lists what falls inside it. MongoDB, Host, IIS and Windows events have no source in this case, so those lanes stay empty.</p>'
  + '<div class="picks" role="radiogroup" aria-label="Minutes around the onset"><span class="note">Minutes around the onset</span>'
  + [2, 5, 15].map(function(v){
    return '<label class="wf"><input type="radio" name="near-min" value="' + v + '"' + (m === v ? ' checked' : '') + ' onchange="nearSet(' + v + ')"> ' + v + ' min</label>';
  }).join('')
  + '</div>'
  + swimlane(lanes, { from: 60, to: 119 }, { from: bFrom, to: bTo })
  + '<p class="note">Events inside the shaded band, ' + hhmm(bFrom) + ' to ' + hhmm(bTo) + ' UTC (' + rows.length + ' of ' + NEAR_ROWS.length + ').</p>'
  + (rows.length
    ? '<div class="tscroll"><table><thead><tr><th>Time (UTC)</th><th>Source</th><th>What happened</th></tr></thead><tbody>'
    + rows.map(function(r){ return '<tr><td>' + r.at + '</td><td>' + r.src + '</td><td>' + r.what + '</td></tr>'; }).join('')
    + '</tbody></table></div>'
    : '<p class="note">Nothing was recorded inside the band.</p>')
  + '</section>';
}

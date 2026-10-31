// Step 5 suspect cards: deterministic fixture rows for "who might be responsible"
// and the suspects the data has already set aside. Named with a "5" suffix because
// a differently-shaped SUSPECTS array (and a differently-shaped ruledOutDrawer
// function) already exist elsewhere in the app; see the brief header.
var SUSPECTS5 = [
  { name:'Blocking on the Orders tables in SQL Server', step:7,
    why:'Lock waits rose at 09:14, the same minute the slow end moved.',
    confidence:'High',
    evidence:[
      'Lock waits on Orders and OrderLines rose from 0.4 s to 31 s per minute at 09:14.',
      'Blocking chains of up to 9 s were recorded between 09:14 and 09:45.',
      'The slowest calls of Orders.Create (up to 4,812 ms) overlap the blocking chains.'
    ] },
  { name:'Release 2026.10.26.2 of Orders.Api', step:5,
    why:'Deployed 9 minutes before the onset, the closest listed change.',
    confidence:'Medium',
    evidence:[
      'Change listed for 09:05; the onset is 09:14, 9 minutes later.',
      'No change after 09:05 is listed.'
    ] },
  { name:'Queueing before the app (outside time)', step:6,
    why:'Outside time grew sharply and holds most of the extra time, but where the wait happens is not in the data.',
    confidence:'Medium',
    evidence:[
      'Outside time (O) rose from 150 ms to 1,584 ms per call.',
      'Outside time holds 85% of the extra time.'
    ] },
  { name:'Index maintenance on the Orders table', step:5,
    why:'Finished well before the onset, so the timing fits less well than the release.',
    confidence:'Low',
    evidence:[
      'Index maintenance finished at 08:40, 34 minutes before the onset.',
      'No lock or error event was recorded between 08:40 and 09:14.'
    ] }
];
var RULED_OUT5 = [
  { name:'Higher load: more calls arriving',
    reason:'Calls arriving per second were about the same (10.2 against 10.4); the system filled because calls took longer, not because more arrived.' },
  { name:'Clock difference between sources',
    reason:'7,204 calls had both a start and an end; the clocks agree within 2 seconds, so a clock gap cannot explain the slow end.' },
  { name:'Larger payloads on Orders.Create',
    reason:'The median request size stayed at 2.1 KB through the window, so a size increase cannot explain the extra 1,680 ms per call.' }
];
function suspectCards(){
  return '<div class="suspects5"><h4 style="font-size:14px">Who might be responsible</h4><div class="scards">'
    + SUSPECTS5.map(function(s){
      return '<article class="scard conf-' + s.confidence.toLowerCase() + '"><h5>' + s.name + '</h5>'
        + '<p class="note">' + s.why + '</p>'
        + '<p><strong>Confidence:</strong> ' + s.confidence + '</p>'
        + '<ul><li>' + s.evidence.join('</li><li>') + '</li></ul>'
        + '<p><a href="#"' + (typeof go === 'function' ? ' onclick="go(' + s.step + ');return false;"' : '') + '>Open the evidence</a></p></article>';
    }).join('')
    + '</div></div>';
}
function ruledOutDrawer5(){
  return '<details class="ev-fold"><summary>Ruled out</summary><ul>'
    + RULED_OUT5.map(function(r){ return '<li><strong>' + r.name + ':</strong> ' + r.reason + '</li>'; }).join('')
    + '</ul></details>';
}

var SLOW_PAGE = 25;
function slowPageRows(rows){
  var n = rows.length;
  var pages = Math.max(1, Math.ceil(n / SLOW_PAGE));
  W.slowPg = Math.min(Math.max(0, W.slowPg || 0), pages - 1);
  return rows.slice(W.slowPg * SLOW_PAGE, W.slowPg * SLOW_PAGE + SLOW_PAGE);
}
function slowPager(total){
  if (total < 1) return '';
  var pages = Math.max(1, Math.ceil(total / SLOW_PAGE));
  var pg = Math.min(Math.max(0, W.slowPg || 0), pages - 1);
  var a = pg * SLOW_PAGE + 1;
  var b = Math.min(total, a + SLOW_PAGE - 1);
  return '<div class="pager" role="navigation" aria-label="Slow calls pages"><span class="note" role="status">Showing ' + a + ' to ' + b + ' of ' + total + '.</span><button class="btn sm" onclick="slowPageGo(-1)"' + (pg === 0 ? ' disabled' : '') + '>Previous</button><button class="btn sm" onclick="slowPageGo(1)"' + (pg >= pages - 1 ? ' disabled' : '') + '>Next</button></div>';
}
function slowPageGo(d){
  W.slowPg = (W.slowPg || 0) + d;
  render();
}
function fpRollupAny(){
  if (typeof fpMs !== 'function') return '';
  var f = W.frame;
  var li = fpMs(f.incidentEnd) - fpMs(f.incidentStart);
  var lr = fpMs(f.referenceEnd) - fpMs(f.referenceStart);
  if (li > 86400000 || lr > 86400000) return '<p class="note" role="status">Showing hourly averages because the window is longer than 24 hours. Spikes shorter than an hour may not show as a peak at this scale.</p>';
  return '';
}

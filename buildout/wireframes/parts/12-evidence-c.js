function evChip(v) {
  if (v === 'ok') return 'Looked normal';
  if (v === 'watch') return 'Worth a look';
  if (v === 'bad') return 'Likely cause';
  return 'Not collected';
}

function evChart(c) {
  if (!c) return '';
  if (c.chart === 'line') {
    var lt = axIncidentT(c.series);
    return tlChart(c.series, lt === null ? [] : [{t:lt, label:'Incident starts'}], axUnit(c.series)) + (typeof fpRollupAny === 'function' ? fpRollupAny() : '') + evChartCaption(c);
  }
  if (c.chart === 'stackedArea') return stackedArea(evAreaSeries(c.series), false, axUnit(c.series), evAreaCols(c.series), axIncidentT(c.series)) + (typeof fpRollupAny === 'function' ? fpRollupAny() : '') + evChartCaption(c);
  if (c.chart === 'bars') return evBars(c.series, axUnit(c.series), axIncidentT(c.series)) + evChartCaption(c);
  return '<p class="note">This was not collected for this case, so there is no chart.</p>';
}

function evTable(c) {
  function x(s) {
    return String(s == null ? '' : s)
      .split('&').join('&amp;')
      .split('<').join('&lt;')
      .split('>').join('&gt;')
      .split('"').join('&quot;');
  }
  if (!c) return '';
  if (typeof evDepBlocked === 'function' && evDepBlocked(c) && typeof evDepBanner === 'function') { return evDepBanner(c); }
  if (!c.table || !c.table.cols || !c.table.cols.length || !c.table.rows || !c.table.rows.length) return '';
  var cols = c.table.cols;
  var rows = c.table.rows;
  var html = '<table class="wf"><thead><tr>';
  for (var i = 0; i < cols.length; i++) {
    html += '<th>' + x(cols[i]) + '</th>';
  }
  html += '</tr></thead><tbody>';
  var max = rows.length < 5 ? rows.length : 5;
  for (var r = 0; r < max; r++) {
    var row = rows[r];
    if (!Array.isArray(row)) {
      html += '<tr></tr>';
      continue;
    }
    html += '<tr>';
    for (var j = 0; j < row.length; j++) {
      html += '<td>' + x(row[j]) + '</td>';
    }
    html += '</tr>';
  }
  html += '</tbody></table>';
  if (typeof evQueryTable === 'function') { html += evQueryTable(c); }
  return html;
}

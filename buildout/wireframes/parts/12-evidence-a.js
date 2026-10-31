function evAreaSeries(series) {
  if (Object.prototype.toString.call(series) !== '[object Array]') {
    return [];
  }

  var names = [];
  var i, j, k;
  var name, seen;

  for (i = 0; i < series.length; i++) {
    if (!series[i] || typeof series[i].name === 'undefined') {
      continue;
    }
    name = series[i].name;
    seen = false;
    for (j = 0; j < names.length; j++) {
      if (names[j] === name) {
        seen = true;
        break;
      }
    }
    if (!seen) {
      names.push(name);
    }
  }

  var byName = [];
  for (i = 0; i < names.length; i++) {
    var pts = [];
    var pass, idx, s, p;
    for (pass = 0; pass < 2; pass++) {
      var window = (pass === 0) ? 'normal' : 'incident';
      for (idx = 0; idx < series.length; idx++) {
        s = series[idx];
        if (!s || s.name !== names[i] || s.win !== window) {
          continue;
        }
        if (!s.pts) {
          continue;
        }
        for (k = 0; k < s.pts.length; k++) {
          p = s.pts[k];
          if (!p) {
            continue;
          }
          if (typeof p.t !== 'number' || typeof p.v !== 'number') {
            continue;
          }
          pts.push({ t: p.t, v: p.v });
        }
      }
    }
    byName.push(pts);
  }

  var cols = [];
  for (i = 0; i < byName.length; i++) {
    for (k = 0; k < byName[i].length; k++) {
      var col = byName[i][k].t;
      seen = false;
      for (j = 0; j < cols.length; j++) {
        if (cols[j] === col) {
          seen = true;
          break;
        }
      }
      if (!seen) {
        cols.push(col);
      }
    }
  }

  cols.sort(function (x, y) { return x - y; });

  if (cols.length === 0) {
    return [];
  }

  var result = [];
  for (i = 0; i < names.length; i++) {
    var pts2 = byName[i];
    var vals = [];
    for (k = 0; k < cols.length; k++) {
      var target = cols[k];
      var value;
      var exact = false;
      for (j = 0; j < pts2.length; j++) {
        if (pts2[j].t === target) {
          value = pts2[j].v;
          exact = true;
          break;
        }
      }
      if (!exact) {
        var bestT = -Infinity;
        var bestV = 0;
        var hasEarlier = false;
        for (j = 0; j < pts2.length; j++) {
          if (pts2[j].t < target && pts2[j].t > bestT) {
            bestT = pts2[j].t;
            bestV = pts2[j].v;
            hasEarlier = true;
          }
        }
        value = hasEarlier ? bestV : 0;
      }
      vals.push(value);
    }

    var cls;
    if (i === 0) {
      cls = 'a';
    } else if (i === 1) {
      cls = 'c';
    } else if (i === 2) {
      cls = 'p';
    } else {
      cls = 'o';
    }

    result.push({ name: names[i], cls: cls, vals: vals });
  }

  return result;
}

function evAreaCols(series) {
  var cols = [];
  if (Object.prototype.toString.call(series) !== '[object Array]') {
    return cols;
  }
  for (var i = 0; i < series.length; i++) {
    var s = series[i];
    if (!s || Object.prototype.toString.call(s.pts) !== '[object Array]') {
      continue;
    }
    for (var k = 0; k < s.pts.length; k++) {
      var p = s.pts[k];
      if (!p) {
        continue;
      }
      if (typeof p.t !== 'number' || typeof p.v !== 'number') {
        continue;
      }
      var seen = false;
      for (var j = 0; j < cols.length; j++) {
        if (cols[j] === p.t) {
          seen = true;
          break;
        }
      }
      if (!seen) {
        cols.push(p.t);
      }
    }
  }
  cols.sort(function (a, b) { return a - b; });
  return cols;
}

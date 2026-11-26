'use strict';
// Sets the saved appearance before the first paint so nothing flashes. Kept as its own file because the page allows no inline script.
try {
  var a = JSON.parse(localStorage.getItem('rm-appearance') || '{}'), r = document.documentElement;
  ['style', 'palette', 'font', 'density', 'mode'].forEach(function (k) { if (typeof a[k] === 'string' && /^[a-z-]+$/.test(a[k])) r.dataset[k] = a[k]; });
} catch (e) { /* private mode or bad JSON: keep the defaults on the page */ }

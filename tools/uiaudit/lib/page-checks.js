// Runs INSIDE the page (injected by audit.spec.ts as plain text, so no build step touches it).
// It defines window.__uiaudit.run(options) which looks at what is on screen and returns a list of findings.
// Nothing here changes the page except `annotate`, which draws outlines on top of it.
//
// A finding: { check, severity: 'error' | 'warn' | 'info', message, label, selector, rect: {x, y, w, h} }
// `label` is a short human name of the element (its words or its aria-label). It is the stable part of the baseline
// fingerprint, so it must not hold a number that changes from run to run.
(function () {
  'use strict';

  var INTERACTIVE = 'a[href],button,input:not([type=hidden]),select,textarea,summary,[role=button],[role=tab],[role=menuitem],[role=switch],[role=checkbox],[role=radio],[role=link],[tabindex]:not([tabindex="-1"])';

  // ---------- small helpers ----------
  function cs(el) { return getComputedStyle(el); }
  function rectOf(el) { return el.getBoundingClientRect(); }
  /**
   * True when a press on the rectangle (x, y, w, h) of `el`'s pseudo-element really goes to `el`. The pseudo-element's computed size and offsets only say
   * where it would be: it may be placed far from the control, cut off by an ancestor (overflow), shrunk by a transform, or have another element on top.
   * So the browser is asked (elementFromPoint, which sees clipping, transforms and stacking order): the centre and the four corners (1 px inside) must lie
   * inside the viewport, and each must answer with `el` or something inside it. A rectangle that does not even touch the control's own box is not a grown
   * hit area but a separate thing, and does not count either.
   */
  function pressGoesTo(el, r, x, y, w, h) {
    var vw = window.innerWidth, vh = window.innerHeight;
    if (x > r.right || x + w < r.left || y > r.bottom || y + h < r.top) return false;
    var d = Math.min(1, w / 4, h / 4);
    var pts = [[x + w / 2, y + h / 2], [x + d, y + d], [x + w - d, y + d], [x + d, y + h - d], [x + w - d, y + h - d]];
    for (var i = 0; i < pts.length; i++) {
      var px = pts[i][0], py = pts[i][1];
      if (px < 0 || py < 0 || px >= vw || py >= vh) return false;
      var hit = document.elementFromPoint(px, py);
      if (!hit || !(hit === el || el.contains(hit))) return false;
    }
    return true;
  }
  /** The box of an element joined with its absolutely positioned, clickable ::before and ::after (the control must be positioned: the pseudo-element is placed against its padding box). Only a pseudo-element that really receives the press counts (see pressGoesTo). */
  function hitArea(el, r) {
    var cs0 = getComputedStyle(el);
    var out = { left: r.left, top: r.top, right: r.right, bottom: r.bottom };
    if (cs0.position !== 'static') {
      ['::before', '::after'].forEach(function (p) {
        var s = getComputedStyle(el, p);
        if (!s || s.content === 'none' || s.content === 'normal' || s.position !== 'absolute' || s.pointerEvents === 'none' || s.display === 'none' || s.visibility === 'hidden') return;
        var w = parseFloat(s.width), h = parseFloat(s.height), l = parseFloat(s.left), t = parseFloat(s.top);
        if (!(w > 0 && h > 0) || isNaN(l) || isNaN(t)) return;
        var x = r.left + (parseFloat(cs0.borderLeftWidth) || 0) + l, y = r.top + (parseFloat(cs0.borderTopWidth) || 0) + t;
        if (!pressGoesTo(el, r, x, y, w, h)) return;
        out = { left: Math.min(out.left, x), top: Math.min(out.top, y), right: Math.max(out.right, x + w), bottom: Math.max(out.bottom, y + h) };
      });
    }
    return { width: out.right - out.left, height: out.bottom - out.top };
  }
  function docRect(r) { return { x: Math.round(r.left + scrollX), y: Math.round(r.top + scrollY), w: Math.round(r.width), h: Math.round(r.height) }; }

  function ownText(el) {
    var s = '';
    for (var n = el.firstChild; n; n = n.nextSibling) if (n.nodeType === 3) s += n.nodeValue;
    return s.replace(/\s+/g, ' ').trim();
  }
  function allText(el) { return (el.textContent || '').replace(/\s+/g, ' ').trim(); }

  function nameOf(el) {
    var a = el.getAttribute('aria-label') || el.getAttribute('title') || el.getAttribute('placeholder') || '';
    var t = a || allText(el) || el.getAttribute('alt') || el.getAttribute('name') || el.id || '';
    t = t.replace(/\s+/g, ' ').trim();
    if (t.length <= 2) { var lab = el.closest('[aria-label],[title]'); if (lab && lab !== el) t = (lab.getAttribute('aria-label') || lab.getAttribute('title')) + ' (' + t + ')'; }
    if (t.length > 48) t = t.slice(0, 45) + '...';
    // A number in a label (a count, a time) would change the baseline key between runs.
    return t.replace(/\d+/g, '#');
  }

  function selectorOf(el) {
    var parts = [];
    for (var cur = el, i = 0; cur && cur.nodeType === 1 && i < 4 && cur !== document.body; cur = cur.parentElement, i++) {
      var p = cur.tagName.toLowerCase();
      if (cur.id) { parts.unshift(p + '#' + cur.id); break; }
      var cls = (typeof cur.className === 'string' ? cur.className : '').trim().split(/\s+/).filter(Boolean).slice(0, 2);
      if (cls.length) p += '.' + cls.join('.');
      parts.unshift(p);
    }
    return parts.join(' > ');
  }

  function isHiddenByStyle(el, s) {
    if (s.display === 'none' || s.visibility === 'hidden' || s.visibility === 'collapse') return true;
    if (parseFloat(s.opacity) === 0) return true;
    return false;
  }

  // Visible = has a box, is not hidden by CSS (own or an ancestor's), and is not a visually-hidden helper (1px clip).
  function visible(el) {
    if (el.nodeType !== 1) return false;
    // checkVisibility also knows closed <details> and content-visibility: hidden, whose boxes still report a size.
    if (el.checkVisibility && !el.checkVisibility({ contentVisibilityAuto: true, opacityProperty: true, visibilityProperty: true })) return false;
    var s = cs(el);
    if (isHiddenByStyle(el, s)) return false;
    var r = rectOf(el);
    if (r.width < 1 || r.height < 1) return false;
    if (s.clip === 'rect(0px, 0px, 0px, 0px)' || s.clipPath === 'inset(50%)') return false;
    for (var a = el.parentElement; a; a = a.parentElement) {
      var as = cs(a);
      if (isHiddenByStyle(a, as)) return false;
      if (a.hasAttribute('inert') || a.getAttribute('aria-hidden') === 'true') {
        // aria-hidden content is still on screen for sighted people (icons); only `inert` panels are off.
        if (a.hasAttribute('inert')) return false;
      }
    }
    return true;
  }

  // Is the element cut away by an ancestor that clips (overflow hidden / auto / scroll) and lying fully outside it?
  function clippedAway(el) {
    var r = rectOf(el);
    for (var a = el.parentElement; a && a !== document.documentElement; a = a.parentElement) {
      var s = cs(a);
      if (s.position === 'fixed' && a !== el) { /* a fixed parent clips nothing by itself */ }
      if (/(hidden|clip|auto|scroll)/.test(s.overflowX + s.overflowY)) {
        var ar = rectOf(a);
        if (r.right <= ar.left + 0.5 || r.left >= ar.right - 0.5 || r.bottom <= ar.top + 0.5 || r.top >= ar.bottom - 0.5) {
          // Outside the clipping box: not on screen. (A scroller's off-screen items count as "not seen".)
          return true;
        }
      }
    }
    return false;
  }

  // The stacking layer an element lives in: its nearest ancestor that is fixed, sticky, a dialog, or positioned with a z-index.
  // A popover or drawer sits on a layer of its own, so what it covers on the page underneath is layering, not a clash.
  function layerOf(el) {
    for (var a = el.parentElement; a && a !== document.body; a = a.parentElement) {
      var s = cs(a);
      if (s.position === 'fixed' || s.position === 'sticky' || a.getAttribute('role') === 'dialog' || a.getAttribute('aria-modal') === 'true' || (s.position !== 'static' && s.zIndex !== 'auto')) return a;
    }
    return null;
  }

  // The box around the words of an element that people can see: text inside visually-hidden helpers (1 px, clipped) is left out.
  function visibleTextRect(el) {
    var l = Infinity, t = Infinity, r = -Infinity, b = -Infinity;
    var walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    for (var n = walker.nextNode(); n; n = walker.nextNode()) {
      if (!n.nodeValue.trim()) continue;
      var pr = rectOf(n.parentElement);
      var ps = cs(n.parentElement);
      if (pr.width <= 2 || pr.height <= 2 || ps.visibility === 'hidden') continue;
      var range = document.createRange();
      range.selectNodeContents(n);
      var rr = range.getBoundingClientRect();
      if (rr.width <= 0) continue;
      l = Math.min(l, rr.left); t = Math.min(t, rr.top); r = Math.max(r, rr.right); b = Math.max(b, rr.bottom);
    }
    if (l === Infinity) return { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 };
    return { left: l, top: t, right: r, bottom: b, width: r - l, height: b - t };
  }

  // A heading's box is as wide as its row; what a control can really cover is the words. Use the text for headings.
  function boxOf(el) {
    if (/^H[1-6]$/.test(el.tagName)) {
      var range = document.createRange();
      range.selectNodeContents(el);
      var r = range.getBoundingClientRect();
      if (r.width > 0 && r.height > 0) return r;
    }
    return rectOf(el);
  }

  function scrollerOf(el) {
    for (var a = el.parentElement; a; a = a.parentElement) {
      var s = cs(a);
      if (s.position === 'fixed') return a;
      if (/(auto|scroll)/.test(s.overflowX + s.overflowY) && (a.scrollHeight > a.clientHeight + 1 || a.scrollWidth > a.clientWidth + 1)) return a;
    }
    return document.documentElement;
  }

  // ---------- colour ----------
  function parseColor(str) {
    if (!str) return null;
    var m = str.match(/rgba?\(([^)]+)\)/);
    if (m) {
      var p = m[1].split(/[ ,/]+/).filter(Boolean).map(parseFloat);
      return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 };
    }
    m = str.match(/color\(srgb ([^)]+)\)/);
    if (m) {
      var q = m[1].split(/[ /]+/).filter(Boolean).map(parseFloat);
      return { r: q[0] * 255, g: q[1] * 255, b: q[2] * 255, a: q.length > 3 ? q[3] : 1 };
    }
    return null;
  }
  function over(top, bottom) { // composite `top` (with alpha) over an opaque `bottom`
    var a = top.a;
    return { r: top.r * a + bottom.r * (1 - a), g: top.g * a + bottom.g * (1 - a), b: top.b * a + bottom.b * (1 - a), a: 1 };
  }
  function lum(c) {
    function ch(v) { v = v / 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); }
    return 0.2126 * ch(c.r) + 0.7152 * ch(c.g) + 0.0722 * ch(c.b);
  }
  function ratio(a, b) {
    var la = lum(a), lb = lum(b);
    var hi = Math.max(la, lb), lo = Math.min(la, lb);
    return (hi + 0.05) / (lo + 0.05);
  }
  // The colour behind an element: walk up, stack the translucent backgrounds, stop at the first opaque one.
  // Returns null when a gradient or picture is in the way (the real colour is not knowable from here).
  function backdrop(el) {
    var layers = [];
    for (var a = el; a; a = a.parentElement) {
      var s = cs(a);
      if (s.backgroundImage && s.backgroundImage !== 'none') return null;
      var c = parseColor(s.backgroundColor);
      if (c && c.a > 0) {
        layers.push(c);
        if (c.a >= 0.999) break;
      }
    }
    var base = { r: 255, g: 255, b: 255, a: 1 };
    if (!layers.length || layers[layers.length - 1].a < 0.999) {
      var page = parseColor(cs(document.body).backgroundColor);
      var root = parseColor(cs(document.documentElement).backgroundColor);
      base = (root && root.a >= 0.999) ? root : (page && page.a >= 0.999) ? page : { r: 255, g: 255, b: 255, a: 1 };
    }
    var out = layers.length && layers[layers.length - 1].a >= 0.999 ? layers.pop() : base;
    while (layers.length) out = over(layers.pop(), out);
    return out;
  }
  function inheritedOpacity(el) {
    var o = 1;
    for (var a = el; a; a = a.parentElement) o *= parseFloat(cs(a).opacity);
    return o;
  }

  // ---------- the checks ----------
  function run(opts) {
    opts = opts || {};
    var phone = innerWidth < 600;
    var findings = [];
    var counts = {};
    var CAP = opts.capPerCheck || 30;

    function add(check, severity, message, el, extra) {
      counts[check] = (counts[check] || 0) + 1;
      if (counts[check] > CAP) return;
      var r = el ? rectOf(el) : { left: 0, top: 0, width: 0, height: 0 };
      var f = { check: check, severity: severity, message: message, label: el === document.documentElement ? 'page' : el ? nameOf(el) : '', selector: el ? selectorOf(el) : '', rect: docRect(r) };
      if (extra) for (var k in extra) f[k] = extra[k];
      findings.push(f);
    }

    var all = Array.prototype.slice.call(document.body.querySelectorAll('*')).filter(function (el) {
      var t = el.tagName;
      if (t === 'SCRIPT' || t === 'STYLE' || t === 'TEMPLATE' || t === 'NOSCRIPT' || t === 'HEAD' || t === 'META' || t === 'LINK') return false;
      if (el.closest('#__uiaudit_overlay')) return false;
      return true;
    });
    var vis = [];
    var visSet = new Set();
    all.forEach(function (el) { if (visible(el)) { vis.push(el); visSet.add(el); } });

    // 1. The page scrolls sideways.
    var root = document.documentElement;
    if (root.scrollWidth > innerWidth + 1) {
      add('page-overflow-x', 'error', 'The page is ' + (root.scrollWidth - innerWidth) + ' px wider than the window, so it scrolls sideways.', root);
      // An element inside a scroller or clip box that itself fits in the window does not widen the page.
      var widens = function (el) {
        for (var a = el.parentElement; a && a !== document.body; a = a.parentElement) {
          if (/(hidden|clip|auto|scroll)/.test(cs(a).overflowX) && rectOf(a).right <= innerWidth + 1) return false;
        }
        return true;
      };
      var seen = 0;
      all.forEach(function (el) {
        if (seen >= 6) return;
        var r = rectOf(el);
        if (r.right > innerWidth + 1 && r.width > 0 && cs(el).position !== 'fixed' && !clippedAway(el) && widens(el)) {
          var p = el.parentElement;
          if (p && p !== document.body && rectOf(p).right > innerWidth + 1 && widens(p)) return; // report the outermost offender only
          seen++;
          add('element-past-edge', 'error', 'Sticks out past the right edge of the window by ' + Math.round(r.right - innerWidth) + ' px' + (visSet.has(el) ? '.' : ' (it cannot be seen, but it still widens the page).'), el);
        }
      });
    }

    // 2. Text cut off, or spilling out of its box.
    vis.forEach(function (el) {
      var s = cs(el);
      var tag = el.tagName;
      if (tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA' || tag === 'svg' || tag === 'CANVAS') return;
      var clips = /(hidden|clip)/.test(s.overflowX);
      var words = allText(el);
      if (!words) return;
      if (clips && el.scrollWidth > el.clientWidth + 1 && el.clientWidth > 0) {
        if (s.textOverflow === 'ellipsis') add('text-truncated', 'info', 'Long text is shortened with "...". The rest is hidden.', el);
        else add('text-clipped', 'error', 'Text is cut off at the edge (' + (el.scrollWidth - el.clientWidth) + ' px hidden) with no "..." to show it.', el);
      } else if (/(hidden|clip)/.test(s.overflowY) && el.scrollHeight > el.clientHeight + 2 && el.clientHeight > 0 && s.webkitLineClamp === 'none' && !/(auto|scroll)/.test(s.overflowY)) {
        add('text-clipped', 'error', 'Content is cut off at the bottom (' + (el.scrollHeight - el.clientHeight) + ' px hidden).', el);
      }
      // text that runs out of its own visible box (button, chip, badge, input wrapper)
      if (!clips && !/(auto|scroll)/.test(s.overflowX) && (parseFloat(s.borderTopWidth) > 0 || (parseColor(s.backgroundColor) || { a: 0 }).a > 0.05) && ownText(el) && s.display !== 'inline') {
        var tr = visibleTextRect(el);
        var er = rectOf(el);
        if (tr.width > 0 && (tr.right > er.right + 1.5 || tr.left < er.left - 1.5)) {
          add('text-spill', 'error', 'Text runs out of its box by ' + Math.round(Math.max(tr.right - er.right, er.left - tr.left)) + ' px.', el);
        }
      }
    });

    // 3. Overlaps. Interactive controls and headings that sit on top of each other (same scroll area, not parent and child).
    var cand = vis.filter(function (el) {
      if (el.matches(INTERACTIVE) || /^H[1-6]$/.test(el.tagName)) {
        var s = cs(el);
        if (s.position === 'absolute' && el.matches('input[type=checkbox],input[type=radio]')) return false;
        var r = rectOf(el);
        return r.width >= 4 && r.height >= 4 && !clippedAway(el);
      }
      return false;
    });
    var ctx = new Map();
    cand.forEach(function (el) { ctx.set(el, scrollerOf(el)); });
    var seenPair = 0;
    for (var i = 0; i < cand.length && seenPair < CAP; i++) {
      for (var j = i + 1; j < cand.length; j++) {
        var A = cand[i], B = cand[j];
        if (ctx.get(A) !== ctx.get(B)) continue;
        if (layerOf(A) !== layerOf(B)) continue;
        if (A.contains(B) || B.contains(A)) continue;
        var ra = boxOf(A), rb = boxOf(B);
        var ix = Math.min(ra.right, rb.right) - Math.max(ra.left, rb.left);
        var iy = Math.min(ra.bottom, rb.bottom) - Math.max(ra.top, rb.top);
        if (ix <= 1 || iy <= 1) continue;
        var inter = ix * iy;
        var small = Math.min(ra.width * ra.height, rb.width * rb.height);
        if (inter < 40 || inter / small < 0.15) continue;
        // a small control sitting on a big surface (a slider thumb on its chart, a badge on a card) is the design, not a clash
        if (Math.max(ra.width * ra.height, rb.width * rb.height) / small > 12) continue;
        // a label wrapped around its own control is one widget
        if (A.closest('label') && A.closest('label') === B.closest('label')) continue;
        seenPair++;
        add('overlap', 'error', 'Overlaps "' + nameOf(B) + '" by ' + Math.round(ix) + ' x ' + Math.round(iy) + ' px.', A, { other: nameOf(B), otherRect: docRect(rb) });
        if (seenPair >= CAP) break;
      }
    }

    // 4. Small tap targets.
    var small = [];
    vis.forEach(function (el) {
      if (!el.matches(INTERACTIVE) || el.matches('[tabindex]') && !el.matches('a[href],button,input,select,textarea,summary,[role]')) return;
      if (el.matches('input[type=hidden]')) return;
      if (el.disabled) return;
      var box = el;
      if (el.matches('input[type=checkbox],input[type=radio]') && el.closest('label')) box = el.closest('label');
      var r = rectOf(box);
      var s = cs(box);
      // A link in the middle of a sentence is exempt (WCAG 2.5.8): inline display inside a text block.
      if (el.tagName === 'A' && s.display === 'inline') return;
      // A hit area drawn by an absolutely positioned ::before or ::after of the control (a click on it is a click on the control) counts
      // as part of the target: the box and the focus ring stay as they are and only the area that answers a press grows.
      var hit = (r.width < 24 || r.height < 24) ? hitArea(box, r) : r;
      if (hit.width < 24 || hit.height < 24) {
        add('small-target', 'warn', 'Tap target is ' + Math.round(hit.width) + ' x ' + Math.round(hit.height) + ' px (the web accessibility rule WCAG 2.2 asks for at least 24 x 24).', el);
        small.push(el);
      } else if (phone && (r.width < 40 || r.height < 36) && s.display !== 'inline') {
        add('small-target', 'info', 'Tap target is ' + Math.round(r.width) + ' x ' + Math.round(r.height) + ' px on a phone (a thumb needs about 44 x 44).', el);
      }
    });

    // 5. Contrast of text against what is behind it (WCAG AA: 4.5, large text 3).
    var skipC = 0;
    vis.forEach(function (el) {
      var txt = ownText(el);
      var isSvgText = el.tagName.toLowerCase() === 'text' || el.tagName.toLowerCase() === 'tspan';
      if (!txt && !isSvgText) return;
      if (isSvgText) txt = allText(el);
      if (!txt) return;
      if (el.closest('[disabled],[aria-disabled="true"]')) return;
      var s = cs(el);
      var fg = parseColor(isSvgText ? s.fill : s.color);
      if (!fg) return;
      var bg = backdrop(el);
      if (!bg) { skipC++; return; }
      var op = inheritedOpacity(el);
      fg = { r: fg.r, g: fg.g, b: fg.b, a: fg.a * op };
      var eff = over(fg, bg);
      var cr = ratio(eff, bg);
      var px = parseFloat(s.fontSize);
      var bold = parseInt(s.fontWeight, 10) >= 700;
      var large = px >= 24 || (px >= 18.66 && bold);
      var need = large ? 3 : 4.5;
      if (cr < need - 0.01) {
        add('low-contrast', cr < 3 ? 'error' : 'warn', 'Text contrast is ' + cr.toFixed(2) + ':1; needs ' + need + ':1.', el, { ratio: Math.round(cr * 100) / 100 });
      }
    });

    // 6. Small text.
    vis.forEach(function (el) {
      var own = ownText(el);
      if (!own) return;
      // a one- or two-character icon glyph ("?", "x") inside a control is a picture, not reading text
      if (own.length <= 2 && el.closest(INTERACTIVE)) return;
      var px = parseFloat(cs(el).fontSize);
      if (px < 11) add('small-text', 'warn', 'Text is ' + px + ' px, smaller than 11 px.', el);
    });

    // 7a. Controls in one row that sit slightly off the same line.
    var parents = new Set();
    cand.forEach(function (el) { if (el.parentElement) parents.add(el.parentElement); });
    parents.forEach(function (p) {
      var kids = Array.prototype.filter.call(p.children, function (k) {
        if (!visSet.has(k)) return false;
        var s = cs(k);
        return k.matches(INTERACTIVE) && s.position !== 'absolute' && s.position !== 'fixed' && !k.matches('input[type=checkbox],input[type=radio]');
      });
      if (kids.length < 2) return;
      // group into rows: controls whose vertical ranges overlap by more than half
      var rows = [];
      kids.forEach(function (k) {
        var r = rectOf(k);
        var row = rows.find(function (g) { var gr = g[0].r; return Math.min(gr.bottom, r.bottom) - Math.max(gr.top, r.top) > Math.min(gr.height, r.height) * 0.5; });
        if (row) row.push({ el: k, r: r }); else rows.push([{ el: k, r: r }]);
      });
      rows.forEach(function (g) {
        if (g.length < 2) return;
        var cs2 = g.map(function (o) { return (o.r.top + o.r.bottom) / 2; });
        var ts = g.map(function (o) { return o.r.top; });
        var bs = g.map(function (o) { return o.r.bottom; });
        function spread(a) { return Math.max.apply(null, a) - Math.min.apply(null, a); }
        var sc = spread(cs2), st = spread(ts), sb = spread(bs);
        if (sc > 1 && st > 1 && sb > 1 && sc <= 8) {
          add('misaligned-row', 'warn', g.length + ' controls in one row are not on a common line (centres differ by ' + sc.toFixed(1) + ' px, tops by ' + st.toFixed(1) + ' px).', g[0].el, { others: g.slice(1).map(function (o) { return nameOf(o.el); }).join(' | ') });
        }
      });
    });

    // 7b. Stacked blocks whose left edge is a few pixels off from its siblings.
    var seenParents = new Set();
    vis.forEach(function (el) {
      var p = el.parentElement;
      if (!p || seenParents.has(p)) return;
      seenParents.add(p);
      var kids = Array.prototype.filter.call(p.children, function (k) {
        if (!visSet.has(k)) return false;
        var s = cs(k);
        var r = rectOf(k);
        return (s.display === 'block' || s.display === 'flex' || s.display === 'grid' || s.display === 'list-item') && s.position !== 'absolute' && s.position !== 'fixed' && r.width >= 40 && r.height >= 12 && s.float === 'none';
      });
      if (kids.length < 3) return;
      // only a true vertical stack: no two kids share a row
      var rs = kids.map(function (k) { return rectOf(k); });
      for (var a = 0; a < rs.length; a++) for (var b = a + 1; b < rs.length; b++) {
        if (Math.min(rs[a].bottom, rs[b].bottom) - Math.max(rs[a].top, rs[b].top) > 2) return;
      }
      // What the eye sees as the left edge: the box when it has a border or a fill, otherwise where its words start.
      var lefts = kids.map(function (k, idx) {
        var s = cs(k);
        var boxed = parseFloat(s.borderLeftWidth) > 0 || (parseColor(s.backgroundColor) || { a: 0 }).a > 0.05;
        if (boxed) return Math.round(rs[idx].left);
        var tr = visibleTextRect(k);
        return tr.width > 0 ? Math.round(tr.left) : null;
      });
      var known = lefts.filter(function (l) { return l !== null; });
      if (known.length < 3) return;
      var tally = {};
      known.forEach(function (l) { tally[l] = (tally[l] || 0) + 1; });
      var mode = Object.keys(tally).sort(function (x, y) { return tally[y] - tally[x]; })[0];
      if (tally[mode] < 3 || tally[mode] / known.length < 0.6) return;
      kids.forEach(function (k, idx) {
        if (lefts[idx] === null) return;
        var d = Math.abs(lefts[idx] - mode);
        if (d >= 2 && d <= 6 && k.tagName !== 'HR') {
          add('misaligned-column', 'warn', 'Left edge is ' + d + ' px off from the ' + tally[mode] + ' blocks stacked with it.', k);
        }
      });
    });

    // 8. Broken pictures, bad words on screen.
    vis.forEach(function (el) {
      if (el.tagName === 'IMG' && el.complete && el.naturalWidth === 0) add('broken-image', 'error', 'A picture did not load.', el);
    });
    var bad = /\b(undefined|NaN|Invalid Date)\b|\[object Object\]|\{\{|\}\}|%s\b|%d\b/;
    vis.forEach(function (el) {
      var t = ownText(el);
      if (t && bad.test(t)) add('bad-text', 'error', 'Shows a code word instead of a value: "' + (t.match(bad) || [''])[0] + '".', el);
      else if (t && /\bnull\b/.test(t) && t.length < 60) add('bad-text', 'warn', 'Shows the word "null".', el);
    });

    // 9. Names and labels a screen reader or a tooltip needs.
    vis.forEach(function (el) {
      var tag = el.tagName;
      if (tag === 'BUTTON' || tag === 'A' && el.hasAttribute('href') || el.getAttribute('role') === 'button') {
        var name = (el.getAttribute('aria-label') || el.getAttribute('title') || allText(el) || '').trim();
        if (!name) {
          var lb = el.getAttribute('aria-labelledby');
          if (lb && document.getElementById(lb)) name = allText(document.getElementById(lb));
        }
        if (!name && el.querySelector('img[alt]:not([alt=""])')) name = 'img';
        if (!name) add('no-name', 'warn', 'A ' + (tag === 'A' ? 'link' : 'button') + ' has no words and no aria-label, so it has no name.', el);
      }
      if ((tag === 'INPUT' && !/^(hidden|submit|button|reset|image)$/.test(el.type)) || tag === 'SELECT' || tag === 'TEXTAREA') {
        var has = el.getAttribute('aria-label') || el.getAttribute('aria-labelledby') || el.getAttribute('title') || (el.labels && el.labels.length) || el.closest('label');
        if (!has) add('no-label', 'warn', 'A form field has no label.', el);
      }
    });
    var ids = {};
    all.forEach(function (el) { if (el.id) { ids[el.id] = (ids[el.id] || []); ids[el.id].push(el); } });
    Object.keys(ids).forEach(function (k) { if (ids[k].length > 1) add('duplicate-id', 'warn', 'The id "' + k + '" is used ' + ids[k].length + ' times.', ids[k][1]); });
    if (!document.title.trim()) add('page-title', 'warn', 'The page has no title.', null);
    if (!document.documentElement.lang) add('page-lang', 'info', 'The page does not say its language (html lang).', null);
    // headings that jump a level
    var hs = vis.filter(function (el) { return /^H[1-6]$/.test(el.tagName); });
    for (var h = 1; h < hs.length; h++) {
      var prev = +hs[h - 1].tagName[1], cur = +hs[h].tagName[1];
      if (cur - prev > 1) add('heading-jump', 'info', 'Heading jumps from h' + prev + ' to h' + cur + '.', hs[h]);
    }

    // 10. A big surface of the wrong brightness for the theme (a white card in dark mode, a dark slab in light mode).
    var dark = lum(backdrop(document.body) || { r: 255, g: 255, b: 255 }) < 0.18;
    vis.forEach(function (el) {
      var t = el.tagName;
      if (t === 'IMG' || t === 'CANVAS' || t === 'VIDEO' || t === 'svg' || t === 'IFRAME') return;
      var s = cs(el);
      var c = parseColor(s.backgroundColor);
      if (!c || c.a < 0.95) return;
      var r = rectOf(el);
      if (r.width * r.height < innerWidth * (opts.baseHeight || innerHeight) * 0.02) return;
      var l = lum(c);
      if (dark && l > 0.8) add('theme-mismatch', 'warn', 'A big bright surface in the dark theme.', el);
      if (!dark && l < 0.08) add('theme-mismatch', 'warn', 'A big dark surface in the light theme.', el);
    });

    return {
      findings: findings,
      totals: counts,
      meta: {
        viewport: { w: innerWidth, h: innerHeight },
        docHeight: root.scrollHeight,
        elements: vis.length,
        contrastSkipped: skipC,
        darkSurface: dark,
        dataTheme: root.getAttribute('data-theme'),
        title: document.title
      }
    };
  }

  // Draws numbered outlines over the findings so a screenshot shows where each one is.
  function annotate(findings) {
    var old = document.getElementById('__uiaudit_overlay');
    if (old) old.remove();
    var box = document.createElement('div');
    box.id = '__uiaudit_overlay';
    box.style.cssText = 'position:absolute;left:0;top:0;width:0;height:0;z-index:2147483647;pointer-events:none';
    var colors = { error: '#ff2d55', warn: '#ff9f0a', info: '#0a84ff' };
    findings.forEach(function (f, i) {
      if (!f.rect || f.rect.w < 1 || f.rect.h < 1) return;
      if (f.severity === 'info') return;
      var d = document.createElement('div');
      d.style.cssText = 'position:absolute;box-sizing:border-box;border:2px solid ' + colors[f.severity] + ';left:' + f.rect.x + 'px;top:' + f.rect.y + 'px;width:' + f.rect.w + 'px;height:' + f.rect.h + 'px';
      var n = document.createElement('span');
      n.textContent = String(i + 1);
      n.style.cssText = 'position:absolute;left:-2px;top:-16px;min-width:16px;height:16px;padding:0 3px;font:700 11px/16px sans-serif;color:#fff;background:' + colors[f.severity] + ';text-align:center';
      d.appendChild(n);
      box.appendChild(d);
    });
    document.body.appendChild(box);
  }

  window.__uiaudit = { run: run, annotate: annotate };
})();

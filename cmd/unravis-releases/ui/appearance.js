'use strict';
// Appearance config: five choices (style, palette, font, density, mode) saved as one JSON in localStorage.
// THEMES (options, labels, defaults) is generated from tokens/tokens.json, so a new option shows up here by itself.
const Appearance = (() => {
  const KEY = 'rm-appearance';
  const GROUPS = [['style', 'Style'], ['palette', 'Status colours'], ['font', 'Fonts'], ['density', 'Row spacing'], ['mode', 'Light or dark']];
  const root = document.documentElement;
  const defaults = () => Object.fromEntries(GROUPS.map(([g]) => [g, THEMES[g].default]));
  const valid = (g, v) => THEMES[g].options.some((o) => o.id === v);
  let cfg = defaults();
  try {
    const saved = JSON.parse(localStorage.getItem(KEY) || '{}');
    for (const [g] of GROUPS) if (valid(g, saved[g])) cfg[g] = saved[g];
  } catch (e) { /* private mode or bad JSON: keep defaults */ }
  const apply = () => { for (const [g] of GROUPS) root.dataset[g] = cfg[g]; };
  const save = () => { try { localStorage.setItem(KEY, JSON.stringify(cfg)); } catch (e) { /* not saved */ } };
  apply();

  const preview = (g, o) => {
    if (g === 'style') return `<span class="pv pv-style" data-style="${o.id}"><i></i><i></i></span>`;
    if (g === 'palette') return `<span class="pv pv-pal" data-palette="${o.id}"><i class="a"></i><i class="b"></i><i class="c"></i><i class="d"></i></span>`;
    if (g === 'font') return `<span class="pv pv-font" data-font="${o.id}">Aa</span>`;
    if (g === 'density') return `<span class="pv pv-dens" data-density="${o.id}"><i></i><i></i><i></i></span>`;
    return `<span class="pv pv-mode">${ico(o.id === 'dark' ? 'moon' : o.id === 'light' ? 'sun' : 'auto')}</span>`;
  };
  const optHtml = (g, o) => `<button type="button" class="ap-o" role="radio" aria-checked="${cfg[g] === o.id}" data-g="${g}" data-o="${o.id}">${preview(g, o)}<span class="ap-t">${esc(o.label)}${o.hint ? `<small>${esc(o.hint)}</small>` : ''}</span></button>`;

  function html(open) {
    const label = THEMES.style.options.find((o) => o.id === cfg.style).label;
    return `<button class="btn-q" id="apb" aria-haspopup="dialog" aria-expanded="${open}" aria-label="Appearance: ${esc(label)}">${ico('palette')}<span class="hide-s">Appearance</span></button>
    <div class="menu right ap" ${open ? '' : 'hidden'} role="dialog" aria-label="Appearance">
      <div class="ap-h"><b>Appearance</b><span class="sub">Saved on this PC. Everything changes at once.</span></div>
      ${GROUPS.map(([g, t]) => `<div class="ap-g" role="radiogroup" aria-label="${t}"><div class="ap-l">${t}</div><div class="ap-opts ap-${g}">${THEMES[g].options.map((o) => optHtml(g, o)).join('')}</div></div>`).join('')}
      <div class="ap-f"><button type="button" class="btn sm" data-ap="copy">${ico('copy')}Copy settings</button><button type="button" class="btn sm ghost" data-ap="reset">Reset to default</button></div>
    </div>`;
  }

  function bind(host, cb) {
    $('#apb', host).onclick = (e) => { e.stopPropagation(); cb.toggle(); };
    $$('.ap-o', host).forEach((b) => (b.onclick = (e) => {
      e.stopPropagation(); // the panel is re-rendered, so the document click-away handler must not see a detached target
      cfg[b.dataset.g] = b.dataset.o; apply(); save(); cb.changed();
      const again = $(`.ap-o[data-g="${b.dataset.g}"][data-o="${b.dataset.o}"]`); if (again) again.focus();
    }));
    $('[data-ap=reset]', host).onclick = (e) => { e.stopPropagation(); cfg = defaults(); apply(); save(); cb.changed(); };
    $('[data-ap=copy]', host).onclick = (e) => {
      e.stopPropagation();
      const text = JSON.stringify(cfg, null, 2);
      const done = () => cb.toast('Appearance settings copied');
      if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(text).then(done, done); else done();
    };
  }
  return { html, bind, get config() { return { ...cfg }; } };
})();

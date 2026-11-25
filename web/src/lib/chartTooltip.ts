/**
 * Chart tooltips under the strict policy (BL-323, docs/desktop/spec/02-security.md 2.27).
 *
 * ECharts' default tooltip is an HTML box: its text goes in as markup and its colour dots and bold text are `style="..."` attributes, which
 * `style-src 'self'` blocks. With `renderMode: 'richText'` the tooltip is drawn on the chart canvas instead, so no markup is written at all.
 * A rich-text formatter returns plain text with `\n` between lines. Bold is `{b|text}`; a coloured mark is `{sN|text}` with the style `sN` named in
 * `textStyle.rich`. Everything that came from data goes through `rt()` first, because `{`, `}` and `|` are the rich-text control characters.
 *
 * What the HTML tooltip had that this one does not: text selection (nobody used it: the box vanishes with the pointer), automatic line wrap of one
 * long line (the HTML box was `white-space: nowrap` too, so nothing changes), and `extraCssText` (rounded corners and no shadow: the canvas box has
 * a 4 px radius and no shadow by default). Colour dots from `item.marker` are drawn by ECharts itself in rich-text mode when it builds the text;
 * a custom formatter that wants a coloured mark names a rich style (see `stripsOption` in resource.ts; BlackboxCharts builds its own the same way).
 */

/** The palette fields the tooltip reads. `Palette` of charts.ts and `OverlayPalette` both fit. */
export interface TipPalette { surface: string; axisLine: string; text: string }

/** Makes a value safe to put in rich text: the control characters become look-alikes, so the text stays readable and cannot open a style tag. */
export const rt = (value: unknown): string =>
  String(value ?? '')
    .replace(/[\r\n\t\v\f\u2028\u2029]+/g, ' ') // HTML folded these into one space; in rich text a newline is a new line and could fake one
    .replace(/[{}|]/g, (char) => (char === '{' ? '｛' : char === '}' ? '｝' : '¦'));

/** Bold text inside a tooltip line (the style `b` is set by `richTip`). */
export const bold = (value: unknown): string => '{b|' + rt(value) + '}';

/** The lines of a tooltip joined in reading order; a line that is null, undefined, false or empty is dropped. */
export const tipLines = (lines: ReadonlyArray<string | null | undefined | false>): string =>
  lines.filter((line): line is string => typeof line === 'string' && line !== '').join('\n');

/** The one tooltip look: the surface colour, a hairline border, 12 px text, rich-text mode. `extra` is merged last (trigger, confine, formatter, position). */
export function richTip<T extends object>(p: TipPalette, extra: T): T & { renderMode: 'richText'; backgroundColor: string; borderColor: string; borderWidth: number; padding: number[]; textStyle: { color: string; fontSize: number; rich: Record<string, object> } } {
  return {
    renderMode: 'richText',
    backgroundColor: p.surface,
    borderColor: p.axisLine,
    borderWidth: 1,
    padding: [6, 10],
    textStyle: { color: p.text, fontSize: 12, rich: { b: { fontWeight: 'bold', fontSize: 12, color: p.text } } },
    ...extra
  };
}

/**
 * A rich-text tooltip is drawn on the chart canvas, so it cannot be taller than the chart (the HTML box could hang out of it). ECharts 6 sets
 * the rich-text line height to 22 px and the box padding is 6 px top and bottom, so a chart of height H holds this many lines (measured in
 * Chromium with 3 and 8 compared groups, tools/uiaudit/csp).
 */
export const TIP_LINE_HEIGHT = 22;
export const TIP_PADDING = 12;
export const tipLineBudget = (chartHeight: number): number => Math.max(3, Math.floor((chartHeight - TIP_PADDING) / TIP_LINE_HEIGHT));

/**
 * The lines of a tooltip that lists rows between a head and a foot, cut to `budget` lines: when the rows do not all fit, the last line that
 * would be a row says how many more there are ("and 3 more"), so nothing is cut off silently.
 */
export function fitTip(head: string[], rows: string[], foot: string[], budget: number): string[] {
  const room = Math.max(0, budget - head.length - foot.length);
  if (rows.length <= room) return [...head, ...rows, ...foot];
  const keep = Math.max(0, room - 1);
  return [...head, ...rows.slice(0, keep), 'and ' + (rows.length - keep) + ' more', ...foot];
}

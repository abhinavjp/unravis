/**
 * The explanation block of the Investigation Timeline (WP-17 follow-up): the fixed words for how to read the
 * chart, what each lane shows and how the axis was worked out, plus the "Timeline details" rows that say where
 * each lane's data came from. Pure TypeScript, no Svelte; `TracingExplain.svelte` draws what this file builds.
 * Every static sentence is linted by the copy lint (tests/timelineExplain.test.cjs).
 */
import type { LintEntry } from '../copy/copylint';
import type { TracingLane, TracingLaneKey } from './model';
import { LANE_NAME, LANE_ORDER } from './lanes';

export const EXPLAIN_TEXT: {
  readTitle: 'How to read this chart';
  readLines: string[];
  lanesTitle: 'What each lane shows';
  axisTitle: 'How this axis was worked out';
  detailsTitle: 'Timeline details';
} = {
  readTitle: 'How to read this chart',
  readLines: [
    'Time runs left to right. Each column is one time slot; the slot size is shown above the chart.',
    'The height of a bar is how many events fell in that slot. The number on the left is the tallest bar.',
    'Red marks are failed requests or errors. Hatched areas mean the source was not read there.',
    'Click and drag across the chart to pick a time range. Nothing is changed in your data.'
  ],
  lanesTitle: 'What each lane shows',
  axisTitle: 'How this axis was worked out',
  detailsTitle: 'Timeline details'
};

export const LANE_SHOWS: Record<'audit' | 'sql' | 'iis', string> = {
  audit: 'Each bar counts API requests recorded in the audit log in that slot. The line shows how slow they were.',
  sql: 'Each bar counts slow or failed database calls in that slot.',
  iis: 'Each bar counts web-server requests in that slot. Errors are shown in red.'
};

/** The three lines of "How this axis was worked out". Empty inputs fall back to plain words. */
export function axisLines(bucketLabel: string, zoneName: string): string[] {
  const zone = zoneName || 'the case time zone';
  const bucket = bucketLabel || 'one slot';
  return [
    'Left axis: a count of events per slot, from 0 up to the tallest bar in that lane. Each lane has its own scale.',
    `Bottom axis: time in ${zone}, in slots of ${bucket}.`,
    'Right axis (API lane): how long requests took, from 0 up to the slowest.'
  ];
}

export interface DetailsRow {
  key: TracingLaneKey;
  name: string;
  where: string;
  when: string;
  whole: string;
  sources: string;
}

/** One row per lane in lane order; a lane the response does not carry is skipped. */
export function detailsRows(lanes: TracingLane[], zoneName: string, formatAt: (iso: string) => string): DetailsRow[] {
  const rows: DetailsRow[] = [];
  for (const key of LANE_ORDER) {
    const lane = lanes.find((item) => item.key === key);
    if (!lane) continue;
    rows.push({
      key,
      name: LANE_NAME[key],
      where: lane.origin === 'live' ? 'Live connection' : 'Imported files',
      when: lane.asOf ? `As of ${formatAt(lane.asOf)} (${zoneName})` : 'Not recorded',
      whole: lane.truncated ? 'Cut short, events may be missing' : lane.complete ? 'Complete' : 'Not complete',
      sources: lane.sources.length ? lane.sources.map((source) => source.label).join(', ') : 'None'
    });
  }
  return rows;
}

/** Every static sentence of the block, in the same shape `laneCopyEntries` uses, for the copy lint. */
export const explainCopyEntries = (): LintEntry[] => [
  { id: 'explain.title.read', text: EXPLAIN_TEXT.readTitle },
  { id: 'explain.title.lanes', text: EXPLAIN_TEXT.lanesTitle },
  { id: 'explain.title.axis', text: EXPLAIN_TEXT.axisTitle },
  { id: 'explain.title.details', text: EXPLAIN_TEXT.detailsTitle },
  ...EXPLAIN_TEXT.readLines.map((text, i) => ({ id: `explain.read.${i + 1}`, text })),
  ...LANE_ORDER.map((key) => ({ id: `explain.shows.${key}`, text: LANE_SHOWS[key] })),
  ...axisLines('15 minutes', 'UTC').map((text, i) => ({ id: `explain.axis.${i + 1}`, text }))
];

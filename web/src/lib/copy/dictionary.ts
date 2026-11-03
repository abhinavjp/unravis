/**
 * The tooltip dictionary (roadmap TR-22): the plain-language text for every tooltip and short label the
 * timeline work needs, behind `explain`. Nothing reads it yet; the first two uses are wired in WP-09.
 * The module has no imports at all (INV-6, no chunk cycles) and it adds no glossary terms to the deck.
 */

/** One dictionary entry: the short label and the plain text shown for it. */
export interface DictEntry {
  /** The short label, as shown on screen. */
  term: string;
  /** The plain tooltip text; the label itself is shown when this is empty. */
  plain: string;
}

/**
 * Every tooltip and short label the timeline needs, keyed by a stable id. The lane ids match `LaneKey` in
 * callTrace.ts; `lane.api-audit` is reserved for a later packet.
 */
export const DICTIONARY: Record<string, DictEntry> = {
  'status.4xx': { term: 'Client errors (4xx)', plain: 'Requests the server turned down, such as a missing page or a refused sign-in.' },
  'status.5xx': { term: 'Server errors (5xx)', plain: 'Requests the server could not finish.' },
  incomplete: { term: 'Missing timing', plain: 'Missing timing (not a failure)' },
  'latency.p50': { term: 'Typical time (P50)', plain: 'Half of the requests finished faster than this.' },
  'latency.p95': { term: 'Slower-end time (P95)', plain: 'About 95 of every 100 requests finished within this time.' },
  'latency.p95Ratio': { term: 'Slower-end spread', plain: 'How much longer the slower end took than the typical request.' },
  'lane.api-audit': { term: 'API audit', plain: 'Requests recorded in the API audit log.' },
  'lane.sql': { term: 'SQL', plain: 'Queries recorded by the SQL source.' },
  'lane.iis': { term: 'IIS', plain: 'Requests recorded in the web server log.' },
  'lane.mongo': { term: 'Mongo', plain: 'Operations recorded by the Mongo source.' },
  'lane.redis': { term: 'Redis', plain: 'Commands recorded by the Redis source.' },
  'lane.other': { term: 'Other sources', plain: 'Events from sources without a lane of their own.' },
  'event.duration': { term: 'Duration', plain: 'How long the event took, as its source recorded it.' },
  'event.time-taken': { term: 'Time taken', plain: 'Observed elapsed time, never API processing time.' },
  'iis.point': { term: 'IIS point', plain: 'Drawn at its timestamp until the source semantics are confirmed.' },
  'gap.time-end': { term: 'Time field', plain: 'The W3C time field is assumed to be the request end.' },
  'gap.resolution': { term: 'Time resolution', plain: 'Log time has one-second resolution.' },
  'attr.client-ip': { term: 'Client IP', plain: 'The address the request came from.' },
  'attr.referer': { term: 'Referer', plain: 'The page that linked to the request.' },
  'attr.cookie': { term: 'Cookie', plain: 'A digest, not the value.' },
  'join.unique': { term: 'One match', plain: 'Exactly one record fits.' },
  'join.ambiguous': { term: 'Several matches', plain: 'More than one record fits, so none is picked.' },
  'join.none': { term: 'No match', plain: 'No record fits.' }
};

/** The plain text for `id`: `plain` when set, the term when `plain` is empty, '' for anything else. Never throws. */
export function explain(id: string): string {
  if (typeof id !== 'string' || !Object.hasOwn(DICTIONARY, id)) return '';
  const entry = DICTIONARY[id];
  return entry.plain || entry.term;
}

import type { CatalogKind } from './fileKinds';

/**
 * What a file is, read from the start of its text, for a file whose NAME says nothing (a web log called `site-1.txt`, an application log called
 * `orders.txt`). Pure: no DOM, no fetch. Only the formats that carry a clear header are recognised; anything else stays "not recognised", so a
 * wrong guess never sends a file to the wrong reader.
 */

/** How much of the start of a file is read. An IIS header and its first rows are far smaller than this. */
export const SNIFF_KIND_BYTES = 64 * 1024;

export function sniffKind(head: string): CatalogKind | '' {
  const text = String(head ?? '').replace(/^﻿/, '');
  if (/^#Software:\s*Microsoft HTTP API/im.test(text)) return 'iis-httperr';
  if (/^#Software:\s*Microsoft Internet Information Services/im.test(text)) return 'iis-w3c';
  if (/^#Fields:.*\b(?:cs-uri-stem|time-taken)\b/im.test(text)) return 'iis-w3c';
  // The block log a log4net setup keeps: a "Logger name:" line, then the type and the times.
  if (/^Logger name:/m.test(text) && /^Server Time:/m.test(text)) return 'app-log';
  return '';
}

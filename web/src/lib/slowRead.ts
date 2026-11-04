/**
 * The "still reading" rule, kept pure so a node test can check it: a read that has run for SLOW_READ_MS shows a second,
 * longer status line in place of the plain one. The Cancel button stays visible in both states.
 */
export const SLOW_READ_MS = 6000;

/** The status line for a read: the plain line at first, the long-wait line once the read has run for a while. */
export const readingLine = (plain: string, slow: boolean, slowText: string): string => (slow ? slowText : plain);

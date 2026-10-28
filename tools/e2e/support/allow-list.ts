// The browser console errors and failed requests a test may see without failing.
// Everything else fails the test. Each entry names the defect (or the by-design
// reason) it belongs to. Keep this the only place these are listed.
//
// A test that provokes an error on purpose (a 409 for a stale tab, say) says so
// with allow() from the fixture, in the test itself. That is not a defect, so it
// does not belong here.

export interface Allowed {
  /** Matched against "<console text> | <request url path>". */
  match: RegExp;
  /** Defect id from .dev/scenarios/defects, or the plain reason. */
  why: string;
}

export const ALLOWED: Allowed[] = [
  // (none yet: add an entry only when a defect makes a clean run print an error)
];

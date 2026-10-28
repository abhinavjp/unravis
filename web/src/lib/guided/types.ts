/**
 * The shapes of the guided analysis. `Result` is what internal/investigate answers (P5); the UI only reads it and never changes a word of it.
 * Every field here mirrors a json tag in that package. A field the engine leaves out when it has no value is optional here.
 */

export type Readiness = 'Ready' | 'Partly ready' | 'Cannot test yet' | 'Failed';
export type Tri = 'yes' | 'no' | 'unknown';
export type SuspectColour = 'red' | 'orange' | 'green' | 'grey';
export type SuspectResult = 'Consistent' | 'Partly consistent' | 'Contradicted' | 'Cannot test yet';

export interface Span { window: string; start: string; end: string; minutes: number; kind: 'short' | 'long' }

export interface Step {
  number: number;
  name: string;
  readiness: Readiness;
  reasons: string[];
  untestedSpans?: Span[];
  nutshell: string;
  findings: string[];
}

export interface Percentiles { n: number; p50?: number; p95?: number; p99?: number; max?: number }

export interface Change {
  known: boolean;
  refMs?: number;
  incMs?: number;
  deltaMs?: number;
  deltaPct?: number;
  meaningful: boolean;
  direction: 'up' | 'down' | 'flat';
  markPercent: number;
  markMs: number;
}

export interface Counts { read: number; noStart: number; incomplete: number; anomalous: number; incident: number; reference: number; outside: number; used: number }
export interface GroupFacts { meanMs?: number; perSec?: number; testableMinutes: number }
export interface RouteFacts { route: string; incident: Percentiles; reference: Percentiles; p95Change: Change }
export interface OverallShift { p50: Change; p95: Change; mean: Change; known: boolean; meaningful: boolean }
export interface Onset { status: 'found' | 'not found' | 'cannot date' | string; t0?: string; basis?: string; refP95Ms?: number; thresholdMs?: number; why: string }
export interface Shape { kind: string; periodMinutes?: number; why: string }
export interface Scope { kind: string; testable: number; changed: number; routes?: string[]; why: string }

export interface PartFacts {
  name: string;
  incN: number;
  refN: number;
  incMeanMs?: number;
  refMeanMs?: number;
  change: Change;
  tail: Change;
  excessMs?: number;
  share?: number;
  tested: boolean;
  why?: string;
}

export interface Parts {
  items: PartFacts[];
  coverage?: number;
  dominant: string;
  dominantWhy: string;
  oTested: boolean;
  oWhy: string;
  iisMatched: number;
  oClamped?: number;
  iisMatchRate?: number;
  iisOffsetMs?: number;
}

export interface Load {
  planningPerSec: number;
  incPerSec?: number;
  refPerSec?: number;
  incWms?: number;
  refWms?: number;
  incL?: number;
  refL?: number;
  incPeak: number;
  refPeak: number;
  lambdaDirection: string;
  wDirection: string;
  lDirection: string;
  plannedL?: number;
  poolLimit?: number;
  plannedFill?: number;
  flatTop: boolean;
  flatTopAt?: number;
  reading: string;
}

export interface Retries { known: boolean; incRetried: number; refRetried: number; incShare?: number; refShare?: number; direction: string }
export interface ClockFacts { checked: number; anomalous: number; share: number; anomaly: Tri }
export interface Coverage { iisRows: number; refShare?: number; incShare?: number; dip: Tri }
export interface TierOnset { tier: string; at?: string; why: string; tested?: boolean }

export interface Minute {
  at: string;
  window: string;
  n: number;
  p50?: number;
  p95?: number;
  a?: number;
  c?: number;
  o?: number;
  inFlight: number;
  peak: number;
  gap?: string;
}

export interface Facts {
  counts: Counts;
  incident: GroupFacts;
  reference: GroupFacts;
  routes: RouteFacts[];
  change: OverallShift;
  onset: Onset;
  shape: Shape;
  scope: Scope;
  parts: Parts;
  load: Load;
  retries: Retries;
  errors: { known: boolean; incServer: number; refServer: number; firstIncServer?: string };
  clock: ClockFacts;
  coverage: Coverage;
  gaps?: Span[];
  tiers: TierOnset[];
  series?: Minute[];
}

export interface Signal { value: Tri; missing?: string; why: string }
export interface ChangeItem { at: string; label: string; relation: string; minutesBefore?: number }
export interface Signals {
  flags: Record<string, Signal>;
  split: { shares: Array<{ name: string; share?: number; meaningful: boolean; tested: boolean }>; dominant: string; note?: string };
  pool: { entered: boolean; limit?: number; plannedL?: number; plannedFill?: number; wouldFill: Tri; atLimit: Tri; reading: string; why: string };
  changes: { entered: boolean; items: ChangeItem[]; leading?: string[]; verdict: Tri; why: string; caveat: string };
}

export interface SuspectEvidence { signal: string; role: string; value: Tri; why: string }

export interface Suspect {
  id: string;
  group: string;
  title: string;
  result: SuspectResult;
  colour: SuspectColour;
  why: string;
  tier?: string;
  onsetAt?: string;
  evidence: SuspectEvidence[];
  nextCheck: string;
  overturn: string;
  needs?: string;
  rank: number;
}

export interface TierRole { tier: string; label: string; role: string; at?: string; why: string }
export interface Origin { status: 'named' | 'no clear origin' | 'not enough data' | string; tier?: string; label?: string; at?: string; roles: TierRole[]; note: string; clockNote: string }
export interface Confidence { word: 'High' | 'Medium' | 'Low' | 'None' | string; why: string; sourcesUsed: string[]; sourcesMissing: string[]; notTested: string[]; overturn: string }

export interface Result {
  version: string;
  thresholds: { version: string; changeMinPercent: number; changeMinMs: number; [key: string]: unknown };
  headline: string;
  facts: Facts;
  signals: Signals;
  steps: Step[];
  suspects: Suspect[];
  notApplicable: Array<{ id: string; group: string; title: string }>;
  origin: Origin;
  confidence: Confidence;
  seriesMinutes: number;
}

export interface Window { start: string; end: string }

/** The body of POST /api/cases/{id}/investigation. */
export interface InvestigationRequest {
  sourceIds: string[];
  incident: Window;
  reference: Window;
  route: string;
  poolLimit?: number;
  releases: Array<{ at: string; label: string }>;
  releasesEntered: boolean;
  includeSeries: boolean;
}

export interface InvestigationRead { requests: number; iisRows: number; truncated: boolean; warnings: string[] }
export interface InvestigationAnswer { result: Result; read: InvestigationRead }

export type ChecklistList = 'todo' | 'doing' | 'learned' | 'ruled_out';

export interface ChecklistItem { id: string; list: ChecklistList; text: string; suspect?: string; order: number; created: string; updated: string }
export interface ChecklistInput { list: ChecklistList; text: string; suspect?: string; order?: number }

export interface SavedViewInput { id?: string; name: string; tool: string; settings: Record<string, unknown> }
export interface SavedView { id: string; name: string; tool: string; saved: string; settings: Record<string, unknown> }

export interface ReportInclude { figures: boolean; suspects: boolean; checklist: boolean; sources: boolean }
export interface ReportAnswer { format: 'html' | 'csv'; filename: string; mime: string; sha256: string; bytes: number; content: string }

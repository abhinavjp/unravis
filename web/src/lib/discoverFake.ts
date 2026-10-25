import { CaseApiError, parseImportJob, type ImportJob } from './cases';
import type { Capabilities, DiscoverClient, DiscoverImportRequest, Finding, Gap, JobRef, SamplerStatus, Scan } from './discover';

/**
 * A client for tests and demos that answers from memory. It has the shape of the real client (discover.ts), records every call, and
 * behaves the way the server is meant to: a scan is running for one answer and then done, a job is queued for one answer and then done or
 * failed. Synthetic data only: nothing here names a real machine.
 */

export const FAKE_SCAN_ID = '9f1c2e7a4b6d8f0a1c3e5a7b9d1f3a5c';

export type FakeOutcome = { rows: number } | { error: CaseApiError } | { duplicate: true };

export interface FakeOptions {
  capabilities?: Capabilities | CaseApiError;
  findings?: Finding[];
  gaps?: Gap[];
  host?: Scan['host'];
  /** How many answers say "running" before the scan is done. */
  runningAnswers?: number;
  progress?: Scan['progress'];
  /** The state the scan ends in. */
  finalState?: Scan['state'];
  startedAt?: string;
  /** What each finding's import ends as. A finding not listed adds 100 rows. */
  outcomes?: Record<string, FakeOutcome>;
  /** An error for a call by name, for the error-state tests. */
  failures?: Partial<Record<keyof DiscoverClient, CaseApiError>>;
  sampler?: SamplerStatus;
}

export interface FakeCall { name: keyof DiscoverClient; args: unknown[] }

export interface FakeClient extends DiscoverClient {
  calls: FakeCall[];
  imports: DiscoverImportRequest[];
}

const hexJobId = (index: number): string => '0'.repeat(24) + index.toString(16).padStart(8, '0');

export function fakeFinding(extra: Partial<Finding> = {}): Finding {
  return {
    id: 'k3m7q2x5v8b4n6c9d2f5g7h3j4', detector: 'iis', kind: 'log-set', importer: 'iis-w3c', host: 'local', display: 'W3SVC2 (u_ex010911.log and 13 more)',
    confidence: 90, shows: 'discover.iis.shows', how: '', sizeBytes: 50 * 1024 * 1024, files: 14, oldest: '2001-09-10T00:00:00Z', newest: '2001-09-11T08:41:00Z',
    needsAdmin: false, cost: 'small', ...extra
  };
}

export function createFakeClient(options: FakeOptions = {}): FakeClient {
  const calls: FakeCall[] = [];
  const imports: DiscoverImportRequest[] = [];
  const findings = options.findings ?? [fakeFinding()];
  const gaps = options.gaps ?? [];
  let scanAnswers = 0;
  const jobFor = new Map<string, { findingId: string; answers: number }>();
  let sampler: SamplerStatus = options.sampler ?? { state: 'idle', since: '', samples: 0, stopReason: '' };

  const note = (name: keyof DiscoverClient, ...args: unknown[]) => {
    calls.push({ name, args });
    const failure = options.failures?.[name];
    if (failure) throw failure;
  };

  const client: FakeClient = {
    calls, imports,
    async capabilities() {
      note('capabilities');
      const caps = options.capabilities ?? { supported: true, elevated: false, os: 'windows' };
      if (caps instanceof Error) throw caps;
      return caps;
    },
    async startScan() {
      note('startScan');
      scanAnswers = 0;
      return FAKE_SCAN_ID;
    },
    async getScan(scanId) {
      note('getScan', scanId);
      scanAnswers++;
      const running = scanAnswers <= (options.runningAnswers ?? 1);
      return {
        state: running ? 'running' : options.finalState ?? 'done',
        startedAt: options.startedAt ?? new Date().toISOString(),
        findings: running ? [] : findings,
        gaps: running ? [] : gaps,
        host: options.host ?? { label: 'local', proposed: true, tz: 'UTC', cores: 8, aliases: [] },
        progress: options.progress ?? []
      };
    },
    async cancelScan(scanId) { note('cancelScan', scanId); },
    async startImports(caseId, request) {
      note('startImports', caseId, request);
      imports.push(request);
      const refs: JobRef[] = request.findingIds.map((findingId, index) => ({ findingId, jobId: hexJobId(index + 1) }));
      refs.forEach((ref) => jobFor.set(ref.jobId, { findingId: ref.findingId, answers: 0 }));
      return refs;
    },
    async getJob(caseId, jobId) {
      note('getJob', caseId, jobId);
      const entry = jobFor.get(jobId);
      if (!entry) throw new CaseApiError('That import was not found.', 404, 'not-found');
      entry.answers++;
      const base = { id: jobId, bytesRead: 0, bytesTotal: 100 };
      if (entry.answers === 1) return parseImportJob({ ...base, state: 'queued' }) as ImportJob;
      if (entry.answers === 2) return parseImportJob({ ...base, state: 'running', bytesRead: 40 }) as ImportJob;
      const outcome = options.outcomes?.[entry.findingId] ?? { rows: 100 };
      if ('error' in outcome) return parseImportJob({ ...base, state: 'failed', status: outcome.error.status, failure: { error: outcome.error.message, reason: outcome.error.reason } }) as ImportJob;
      if ('duplicate' in outcome) return parseImportJob({ ...base, state: 'failed', status: 409, failure: { error: 'Already imported.', reason: 'duplicate', importId: 2 } }) as ImportJob;
      return parseImportJob({
        ...base, state: 'done', bytesRead: 100, status: 200,
        result: { importer: 'iis-w3c', score: 1, tied: [], record: { id: 3, importer: 'iis-w3c', fileName: 'x.log', sha256: 'f'.repeat(64), bytes: 100, rows: outcome.rows, rejected: 0, hostLabel: 'local', tzRule: 'utc-field', quality: {}, knownGaps: [], imported: '2001-09-11T09:00:00Z', status: 'ok', supplied: {} } }
      }) as ImportJob;
    },
    async cancelJob(caseId, jobId) { note('cancelJob', caseId, jobId); },
    async startSampler(caseId, request) {
      note('startSampler', caseId, request);
      sampler = { state: 'recording', since: '2001-09-11T08:00:00Z', samples: 0, stopReason: '' };
      return sampler;
    },
    async getSampler(caseId) {
      note('getSampler', caseId);
      if (sampler.state === 'recording') sampler = { ...sampler, samples: sampler.samples + 4 };
      return sampler;
    },
    async stopSampler(caseId) {
      note('stopSampler', caseId);
      sampler = { ...sampler, state: 'stopped', stopReason: '' };
    }
  };
  return client;
}

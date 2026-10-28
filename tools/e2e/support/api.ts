// A small client for the demo's HTTP API. Every call carries the session token;
// mutating case calls carry the state the page believes the case is in.
export interface ApiResult<T = any> {
  status: number;
  json: T;
  text: string;
  headers: Headers;
}

export interface ApiOptions {
  /** Token to send. Omit for the session token, pass null to send none, or a string for a wrong one. */
  token?: string | null;
  headers?: Record<string, string>;
  /** Send the body as it is (a string or a Buffer) with this content type. */
  rawContentType?: string;
}

export type ApiFn = <T = any>(method: string, path: string, body?: unknown, state?: string, opts?: ApiOptions) => Promise<ApiResult<T>>;

export function makeApi(base: string, token: string): ApiFn {
  return async (method, path, body, state, opts = {}) => {
    const headers: Record<string, string> = { ...(opts.headers ?? {}) };
    const t = opts.token === undefined ? token : opts.token;
    if (t !== null) headers['X-Audit-Token'] = t;
    if (state !== undefined) headers['X-Expect-State'] = state;
    let payload: BodyInit | undefined;
    if (body instanceof FormData) {
      payload = body;
    } else if (opts.rawContentType !== undefined) {
      headers['Content-Type'] = opts.rawContentType;
      payload = body as string;
    } else if (body !== undefined) {
      headers['Content-Type'] = 'application/json';
      payload = JSON.stringify(body);
    }
    const res = await fetch(base + path, { method, headers, body: payload, redirect: 'manual' });
    const text = await res.text();
    let json: any = null;
    try {
      json = text ? JSON.parse(text) : null;
    } catch {
      json = null;
    }
    return { status: res.status, json, text, headers: res.headers };
  };
}

/** The standard acknowledgement text the import route compares against. */
export const BANNER =
  'Parsers are built and tested on synthetic files only. Importing real client exports needs: R2 preservation, R8 content review, R11 AI/MCP approval, R12 DBA dry run (SQL files), R13 Mongo access, R16 retention.';

/** The host label the demo imports use. It has to exist in the host map first. */
export const HOST = 'web-a';

let counter = 0;
/** A request id that is valid (8 to 64 of A-Za-z0-9_-) and unique inside one run. */
export function requestId(tag: string): string {
  counter += 1;
  return ('e2e-' + tag + '-' + String(counter).padStart(4, '0')).slice(0, 64);
}

/** Create a case through the API. Returns its id. */
export async function createCase(api: ApiFn, title: string): Promise<string> {
  const r = await api('POST', '/api/cases', { title, requestId: requestId('case') });
  if (r.status !== 201 && r.status !== 200) throw new Error('create case failed: ' + r.status + ' ' + r.text);
  return r.json.id as string;
}

/** Make sure the demo host map knows the host label the access-log imports use. */
export async function ensureHost(api: ApiFn): Promise<void> {
  const r = await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: HOST, role: 'iis', tz: 'UTC' }] });
  if (r.status !== 200) throw new Error('host map failed: ' + r.status + ' ' + r.text);
}

export interface ImportOptions {
  name?: string;
  importer?: string;
  host?: string;
  state?: string;
  /** Send the ack fields (default true). */
  ack?: boolean;
}

/** Import a text file into a case. The ack fields go before the file, as the route needs. */
export async function importFile(api: ApiFn, caseId: string, content: string | Uint8Array, opts: ImportOptions = {}): Promise<ApiResult> {
  const form = new FormData();
  if (opts.ack !== false) {
    form.append('ackGating', 'true');
    form.append('ackText', BANNER);
  }
  form.append('file', new Blob([content as BlobPart]), opts.name ?? 'sample.log');
  const q = new URLSearchParams({ importer: opts.importer ?? 'nginx-access', host: opts.host ?? HOST });
  return api('POST', '/api/cases/' + caseId + '/imports?' + q.toString(), form, opts.state ?? 'open');
}

/** One backslash, built from its code so no source file needs an escaped backslash. */
export const BS = String.fromCharCode(92);

/** The same path with forward slashes (the archive route reads them on Windows too). */
export function fwd(p: string): string {
  return p.split(BS).join('/');
}

/** The full names of the demo cases, by the short code (first 8 letters) the scenarios use. */
const DEMO_NAMES: Record<string, string> = {
  demoopen: 'demoopen',
  democlos: 'democlosed',
  demostal: 'demostale',
  demofail: 'demofailed',
  demomiss: 'demomissing',
  demountr: 'demountrusted',
  demokeye: 'demokeyed',
  demoarch: 'demoarchived',
  demofree: 'demofreed'
};

/** The full 26-character id of a demo case: its name padded with the letter a. */
export function demoId(code: string): string {
  return (DEMO_NAMES[code] ?? code).padEnd(26, 'a');
}

/** Wait until a case reaches a state (archiving and verifying run in the background). */
export async function waitState(api: ApiFn, caseId: string, state: string): Promise<any> {
  const { expect } = await import('@playwright/test');
  let last: any = null;
  await expect
    .poll(
      async () => {
        const r = await api('GET', '/api/cases/' + caseId);
        last = r.json;
        return r.json?.state;
      },
      { message: 'case ' + caseId + ' state', timeout: 30_000 }
    )
    .toBe(state);
  return last;
}

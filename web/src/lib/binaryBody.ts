/**
 * Reading the body of a packed chart answer. The server never sends more than 16 MiB, so a body bigger than that is refused
 * while it is read (the declared Content-Length first, then the bytes counted as they arrive), and a connection that drops
 * mid-body gives null, never a thrown error, so the caller takes its one JSON retry. An aborted read still throws: the
 * caller's signal covers the retry too.
 */

/** The server's own response limit (16 MiB); the packed frames are never larger. */
export const SERIES_BODY_CAP = 16 << 20;

const isAbort = (error: unknown): boolean => error instanceof DOMException && error.name === 'AbortError';

export async function readBinaryBody(response: Response, signal?: AbortSignal, cap: number = SERIES_BODY_CAP): Promise<ArrayBuffer | null> {
  const declared = Number(response.headers.get('Content-Length'));
  const drop = (): void => { void response.body?.cancel().catch(() => undefined); };
  if (Number.isFinite(declared) && declared > cap) { drop(); return null; }
  try {
    const reader = response.body?.getReader();
    if (!reader) {
      const whole = await response.arrayBuffer();
      return whole.byteLength > cap ? null : whole;
    }
    const chunks: Uint8Array[] = [];
    let total = 0;
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.byteLength;
      if (total > cap) { void reader.cancel().catch(() => undefined); return null; }
      chunks.push(value);
    }
    const out = new Uint8Array(total);
    let at = 0;
    for (const chunk of chunks) { out.set(chunk, at); at += chunk.byteLength; }
    return out.buffer;
  } catch (error) {
    if (signal?.aborted || isAbort(error)) throw error;
    return null;
  }
}

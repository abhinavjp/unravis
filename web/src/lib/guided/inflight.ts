/** Keys of requests that are on their way. A second press on the same key is ignored until the first finishes. */
export class InFlight {
  #keys = new Set<string>();

  /** True when the caller may go ahead; false when the same key is already on its way. */
  start(key: string): boolean {
    if (this.#keys.has(key)) return false;
    this.#keys.add(key);
    return true;
  }

  finish(key: string): void {
    this.#keys.delete(key);
  }

  has(key: string): boolean {
    return this.#keys.has(key);
  }
}

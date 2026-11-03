import type { Component } from 'svelte';
import LazyHost from './components/LazyHost.svelte';

/**
 * Wraps a dynamic import so a component can be used in a template exactly like a static import: <Panel onClose={...} />.
 * The panel's code (and the copy text only it uses) is fetched the first time it renders and is then kept. Nothing is drawn while it loads.
 *
 */
export function lazyComponent<T extends Component<any>>(importer: () => Promise<{ default: T }>): T {
  let pending: Promise<T> | null = null;
  let resolved: T | null = null;
  const load = (): Promise<T> => pending ??= importer().then(module => { resolved = module.default; return resolved; }, error => { pending = null; throw error; });
  const wrapper = (anchor: Parameters<T>[0], props: Record<string, unknown>) => {
    return LazyHost(anchor, { load, get props() { return props; }, ready: resolved });
  };
  return wrapper as unknown as T;
}

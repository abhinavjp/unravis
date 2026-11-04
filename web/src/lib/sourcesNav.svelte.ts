/**
 * A request to open the Sources tab of the Sources hub (packet P11). The Investigation Timeline asks for it from its time-zone notice; the hub
 * reads the request when it is on screen and clears it. In memory only.
 */
export const sourcesNav = $state<{ wantList: boolean; wantTab: '' | 'sql' | 'mongo-add' | 'day' }>({ wantList: false, wantTab: '' });

export function requestSourcesList(): void { sourcesNav.wantList = true; }

/** The import drawer's Add data cards send the person to the Sources hub of the case page: the SQL tab, the page that adds a Mongo audit database, or Add another day. */
export function requestSourcesTab(tab: 'sql' | 'mongo-add' | 'day'): void { sourcesNav.wantTab = tab; }

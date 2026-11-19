import { CaseApiError, json, send } from './cases';
import { WIZ } from './copy/sourceWizardCopy';

export type OpenTarget = { path: string } | { scan: string; finding: string };

/** Opens a folder in Explorer on this PC (loopback, validated on the server). Resolves with a plain line when it could not. */
export async function openFolder(target: OpenTarget): Promise<{ opened: true } | { opened: false; line: string }> {
  try {
    const answer = await json<{ opened?: boolean; reason?: string }>(await send('/local/open-folder', { method: 'POST', body: JSON.stringify(target) }));
    return answer.opened ? { opened: true } : { opened: false, line: WIZ.openReasons['cannot-open'] };
  } catch (error) {
    if (error instanceof CaseApiError) {
      const own = Object.prototype.hasOwnProperty.call(WIZ.openReasons, error.reason);
      return { opened: false, line: own ? WIZ.openReasons[error.reason] : error.message };
    }
    return { opened: false, line: WIZ.openReasons['cannot-open'] };
  }
}


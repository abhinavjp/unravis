<script lang="ts" module>
  import type { InstallAnswer } from '../../lib/blackbox';

  export type ResultOutcome =
    | { kind: 'installed'; answer: InstallAnswer }
    | { kind: 'pack'; fileName?: string }
    | { kind: 'failed'; where: 'pc' | 'pack'; error: unknown };
</script>

<script lang="ts">
  // The result of pressing the last button of the Place a Blackbox pop-up (TSK-16). It shows only what really happened:
  //   installed  the server answered 201 with an InstallAnswer (service installed, entry listed, read key sealed)
  //   pack       the zip was built and handed to the browser
  //   failed     the server or the network refused; the words come from the refusal (what and next first, then the
  //              reason table, then the server's own text). One reason is a partial outcome: key-not-saved means the
  //              service IS installed but its read key was not saved. It is never described as undone.
  // The footer buttons (Done, Back to review) belong to the wizard; only actions that fit the content live here.
  import { BlackboxError, packFileName, refusalText } from '../../lib/blackbox';
  import type { Settings } from '../../lib/blackbox';
  import { BB_REFUSAL, BB_TEXT as T } from '../../lib/copy/blackboxCopy';
  import { BBW_TEXT as W } from '../../lib/copy/blackboxWizardCopy';

  let {
    outcome,
    settings,
    onDownloadAgain,
    onChangeFolder
  }: {
    outcome: ResultOutcome;
    /** The payload that was sent (buildSettings). Its interval and folder are shown. */
    settings: Settings;
    /** Pack only: saves the zip again. Without it the button is not shown. */
    onDownloadAgain?: () => void;
    /** Pack and failed only: goes back to the Folder step. Not offered once a Blackbox is placed, because nothing can be moved after that. */
    onChangeFolder?: () => void;
  } = $props();

  const packFile = $derived(outcome.kind === 'pack' ? (outcome.fileName ?? packFileName(settings.name)) : '');
  const refusal = $derived(outcome.kind === 'failed' && outcome.error instanceof BlackboxError ? outcome.error.refusal : null);
  const partial = $derived(refusal?.reason === 'key-not-saved');
  // The server undoes a failed install before it answers 422 or 500. For any other failure (busy, too many tries, no
  // contact) nothing is claimed either way.
  const undone = $derived(outcome.kind === 'failed' && outcome.where === 'pc' && !partial && refusal !== null && (refusal.status === 422 || refusal.status === 500));
  const lostContact = $derived(outcome.kind === 'failed' && outcome.where === 'pc' && refusal !== null && refusal.reason === 'offline');
  const failedWords = $derived(outcome.kind === 'failed' ? refusalText(outcome.error, BB_REFUSAL) : '');
  const failedTitle = $derived(
    partial ? W['result.partialTitle'] : outcome.kind === 'failed' && outcome.where === 'pack' ? W['result.packFailedTitle'] : T.installFailedTitle
  );
</script>

{#snippet folderRow(path: string, change: boolean)}
  <div class="folder">
    <code>{path}</code>
    {#if change && onChangeFolder}
      <button type="button" class="change" onclick={onChangeFolder}>
        {W['result.changeFolder']}<span class="visually-hidden"> {W['result.folderHere']}</span>
      </button>
    {/if}
  </div>
{/snippet}

<div class="result">
  {#if outcome.kind === 'installed'}
    <p role="status" class="lead">{W['result.placed'](outcome.answer.name, settings.intervalSeconds)}</p>
    <section class="card" aria-labelledby="rs-next">
      <h3 id="rs-next">{W['result.next']}</h3>
      <dl>
        <dt>{W['result.status']}</dt>
        <dd>{W['result.readySoon']}</dd>
        <dt>{W['result.savedTo']}</dt>
        <dd>{@render folderRow(outcome.answer.folder || settings.folder, false)}</dd>
        <dt>{W['result.readKey']}</dt>
        <dd>{outcome.answer.entry.hasKey ? W['result.readKeySealed'] : W['result.readKeyMissing']}</dd>
        <dt>{W['result.cases']}</dt>
        <dd>{W['result.casesText']}</dd>
      </dl>
    </section>
    <p class="note">{T.installedKeyNote}</p>
  {:else if outcome.kind === 'pack'}
    <p role="status" class="lead">{W['result.packReady'](packFile)}</p>
    <p class="note">{T.packInside}</p>
    <ol class="steps">
      <li>{W['result.packStep1']}</li>
      <li>{W['result.packStep2']}</li>
      <li>
        {W['result.packFolder'](settings.folder)}
        {#if onChangeFolder}
          <button type="button" class="change" onclick={onChangeFolder}>
            {W['result.changeFolder']}<span class="visually-hidden"> {W['result.folderHere']}</span>
          </button>
          <span class="note">{W['result.packRebuildNote']}</span>
        {/if}
      </li>
      <li>{W['result.packStep4']}</li>
    </ol>
    <p class="note">{T.packKeyNote}</p>
    <p class="note">{T.packBindNote}</p>
    <p class="note">{T.packUninstall}</p>
    {#if onDownloadAgain}
      <div class="actions"><button type="button" class="secondary-button" onclick={onDownloadAgain}>{T.packAgain}</button></div>
    {/if}
  {:else}
    <div class="failed" role="alert" class:partial>
      <h3>{failedTitle}</h3>
      <p>{failedWords}</p>
      {#if partial}
        <p>{W['result.partialNext']}</p>
      {:else if undone}
        <p>{T.installUndone}</p>
      {:else if lostContact}
        <p>{W['result.unknownOutcome']}</p>
      {/if}
    </div>
    {#if onChangeFolder && !partial}
      <div class="folder-row">
        <span class="note">{W['review.folderLabel']}</span>
        {@render folderRow(settings.folder, true)}
      </div>
    {/if}
  {/if}
</div>

<style>
  .result { display: grid; gap: var(--space-3); min-width: 0; }
  .lead { margin: 0; font-size: var(--fs-4); font-weight: 600; overflow-wrap: anywhere; }
  .card { display: grid; gap: var(--space-2); padding: var(--space-4); border: 1px solid var(--hairline); border-radius: var(--radius-md); background: var(--surface); }
  h3 { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  dl { display: grid; grid-template-columns: minmax(96px, 140px) minmax(0, 1fr); gap: var(--space-2) var(--space-3); margin: 0; }
  dt { color: var(--text-2); }
  dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
  code { font-family: var(--font-mono, ui-monospace, monospace); overflow-wrap: anywhere; }
  .folder { display: flex; align-items: center; flex-wrap: wrap; gap: var(--space-2); min-width: 0; }
  .folder-row { display: grid; gap: var(--space-1); }
  .steps { margin: 0; padding-left: var(--space-5); display: grid; gap: var(--space-2); line-height: 1.5; }
  /* The folder step: the path, then Change, then what a change does, one under the other. */
  .steps li > .change { display: flex; margin-left: calc(-1 * var(--space-2)); }
  .steps li > .note { display: block; }
  .note { margin: 0; color: var(--text-2); line-height: 1.5; overflow-wrap: anywhere; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .failed { display: grid; gap: var(--space-2); padding: var(--space-4); border: 1px solid var(--warn); border-left-width: 4px; border-radius: var(--radius-md); background: var(--surface); }
  .failed p { margin: 0; overflow-wrap: anywhere; }
  .change { display: inline-flex; align-items: center; justify-content: center; min-width: 40px; min-height: 40px; padding: 0 var(--space-2); border: 0; background: transparent; color: var(--accent); font: inherit; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .change:hover { background: var(--surface-2); border-radius: var(--radius-sm); }
  .change:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius-sm); }
  @media (max-width: 480px) {
    dl { grid-template-columns: minmax(0, 1fr); gap: var(--space-1); }
    dd { margin-bottom: var(--space-2); }
  }
</style>

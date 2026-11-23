<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import { blackboxApi, refusalText, type BlackboxItem } from '../lib/blackbox';
  import { copyKeyOnce, isKeeperHeldError, KEY_SHOW_MS, makeRevealGuard } from '../lib/blackboxKey';
  import { BB_REFUSAL, BB_TEXT as T } from '../lib/copy/blackboxCopy';

  // Screen 65: the read key, masked, with Show and Copy. It is only for the Blackbox installed on this PC. The key lives in
  // this component's own variable while it is on view. It is not written to storage, to the app state or to a case, and it
  // is let go when it is hidden, after a short time, and when this screen closes.
  let { item }: { item: BlackboxItem } = $props();

  let key = $state('');
  let busy = $state('');
  let note = $state('');
  let problem = $state('');
  let held = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let showBtn = $state<HTMLButtonElement>();
  let hideBtn = $state<HTMLButtonElement>();
  // Show and Hide are two different buttons, so the one that was pressed is gone when the other appears. Focus follows
  // the swap (BLACKBOX-02), or the next Esc would close the whole panel.
  async function followFocus(to: () => HTMLButtonElement | undefined) { await tick(); to()?.focus({ preventScroll: true }); }
  // A Show whose answer lands after Hide, the timer or closing this screen must not put the key back.
  const guard = makeRevealGuard();

  function letGo() {
    guard.cancel();
    key = '';
    if (timer) { clearTimeout(timer); timer = undefined; }
  }
  onDestroy(letGo);

  async function show() {
    if (busy) return;
    busy = T.keyWorking; note = ''; problem = '';
    const mine = guard.next();
    try {
      const got = await blackboxApi.revealKey(item.id);
      if (!guard.valid(mine)) return;
      key = got;
      const at = document.activeElement;
      if (!at || at === document.body || at === showBtn || (at instanceof HTMLElement && at.classList.contains('shell'))) void followFocus(() => hideBtn);
      timer = setTimeout(() => { const had = !!hideBtn && document.activeElement === hideBtn; letGo(); note = T.keyHiddenAgain; if (had) void followFocus(() => showBtn); }, KEY_SHOW_MS);
    } catch (error) {
      if (!guard.valid(mine)) return;
      letGo();
      if (isKeeperHeldError(error)) held = true;
      else problem = refusalText(error, { ...BB_REFUSAL, other: T.keyFailed });
    } finally {
      busy = '';
    }
  }
  function hide() { letGo(); note = ''; void followFocus(() => showBtn); }
  async function copy() {
    if (busy) return;
    busy = T.keyWorking; note = ''; problem = '';
    const done = await copyKeyOnce(() => key ? Promise.resolve(key) : blackboxApi.revealKey(item.id), (text) => navigator.clipboard.writeText(text));
    busy = '';
    if (done === 'copied') note = T.keyCopied;
    else if (done === 'keeper-held') held = true;
    else problem = done === 'no-key' ? T.keyFailed : T.keyCopyFailed;
  }
</script>

<section class="bb-key" aria-label={T.keyTitle}>
  <h3>{T.keyTitle}</h3>
  <p class="bb-key-value">{#if key}<code>{key}</code>{:else}<span aria-hidden="true">••••••••••••••••</span><span class="bb-sr">{T.keyHiddenNow}</span>{/if}</p>
  <p class="bb-note">{T.keyHereOnly}</p>
  <div class="bb-actions">
    {#if key}
      <button type="button" class="secondary-button" bind:this={hideBtn} onclick={hide}>{T.keyHide}</button>
    {:else}
      <button type="button" class="secondary-button" bind:this={showBtn} disabled={!!busy || held} onclick={show}>{T.keyShow}</button>
    {/if}
    <button type="button" class="secondary-button" disabled={!!busy || held} onclick={copy}>{T.keyCopy}</button>
  </div>
  <div>
    {#if key}<p class="bb-sr" role="status">{T.keyShownAnnounce}</p>{/if}
    {#if busy}<p class="bb-note" role="status">{busy}</p>{/if}
    {#if note}<p class="bb-ok" role="status">{note}</p>{/if}
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
    {#if held}<p class="bb-note" role="status">{T.keyHeld}</p>{/if}
  </div>
</section>

<style>
  .bb-key { display: grid; gap: var(--space-2); padding-top: var(--space-3); border-top: 1px solid var(--line); min-width: 0; }
  .bb-key h3 { margin: 0; font-size: 14px; font-weight: 600; }
  .bb-key p { margin: 0; font-size: 13px; line-height: 1.5; }
  .bb-key-value { overflow-wrap: anywhere; word-break: break-all; }
  .bb-key-value code { font-family: var(--font-mono, ui-monospace, monospace); font-size: 12px; }
  .bb-key p.bb-note { font-size: 12px; }
</style>

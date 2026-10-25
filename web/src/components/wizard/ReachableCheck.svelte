<script lang="ts">
  import { addByAddress, sourcesView } from '../../lib/wizardSources.svelte';
  import { ADDRESS_KINDS } from '../../lib/wizardSources';
  import type { AddressKind } from '../../lib/wizardSources';
  import { SOURCES_TEXT, addressProblemText, kindLabel, reachedText, unreachedText } from '../../lib/copy/wizardSourcesCopy';

  // SQL Server or Redis on another computer, by its address. The address is checked for a reply on that port; it is added either way, and one
  // that did not answer stays on the list, marked, so the person decides. No user name or password is asked for here.
  let { kind, onDone }: { kind: AddressKind; onDone?: () => void } = $props();

  const uid = $props.id();
  let text = $state('');
  let busy = $state(false);
  let line = $state('');
  let failed = $state(false);
  let field = $state<HTMLInputElement>();

  const title = $derived(kind === 'redis' ? SOURCES_TEXT.addRedis : SOURCES_TEXT.addSql);

  async function add() {
    if (busy || !ADDRESS_KINDS.includes(kind)) return;
    busy = true; failed = false; line = SOURCES_TEXT.addrChecking;
    const result = await addByAddress(kind, text);
    busy = false;
    if (!result.ok) {
      failed = true;
      line = result.problem === 'duplicate' ? SOURCES_TEXT.alreadyChosen : result.problem === 'full' ? SOURCES_TEXT.limitFull : addressProblemText(result.problem);
      field?.focus();
      return;
    }
    const shown = text.trim();
    text = '';
    failed = result.reached !== true;
    const answer = sourcesView.reach[result.key];
    line = result.reached === true ? reachedText(shown) : unreachedText(shown, answer?.state === 'failed' ? answer.reason : 'unknown') + ' ' + SOURCES_TEXT.addrFlagNote;
    onDone?.();
  }

  function onKey(event: KeyboardEvent) {
    if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); event.stopPropagation(); void add(); }
  }
</script>

<div class="box">
  <label for="{uid}-addr">{title}: {SOURCES_TEXT.addrLabel.toLocaleLowerCase()}</label>
  <input id="{uid}-addr" type="text" bind:this={field} bind:value={text} maxlength="260" autocomplete="off" autocapitalize="off" spellcheck="false"
    aria-describedby="{uid}-hint {uid}-line" aria-invalid={failed && line !== '' ? true : undefined} onkeydown={onKey} />
  <p class="hint" id="{uid}-hint">{SOURCES_TEXT.addrHint}</p>
  <div class="actions"><button type="button" class="primary-button" disabled={busy} aria-label="{SOURCES_TEXT.addrCheck}: {kindLabel(kind)}" onclick={() => void add()}>{busy ? SOURCES_TEXT.addrChecking : SOURCES_TEXT.addrCheck}</button></div>
  <p class="line" class:bad={failed} id="{uid}-line" role="status" hidden={line === ''}>{line}</p>
</div>

<style>
  .box { display: grid; gap: var(--space-2); min-width: 0; max-width: 640px; }
  .box label { font-size: var(--fs-3); font-weight: 600; }
  .box input { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 44px; padding: 8px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-4); }
  .box input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .box input[aria-invalid='true'] { border-color: var(--bad); }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .line { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .line.bad { color: var(--text); }
  .line[hidden] { display: none; }
</style>

<script lang="ts">
  // The Choose lanes picker: every lane the Blackbox records, grouped, searchable, at most 10 shown at once.
  // Changes apply right away (the parent holds the list). It is a native dialog, so Esc and focus return come for free.
  import { BBL_TEXT } from '../lib/copy/bbLanesCopy';
  import { GROUPS, LANES, MAX_LANES, laneMatches, laneName, laneWhat } from '../lib/tracing/bbLanes';

  let { open, chosen, colour, onToggle, onClose }: {
    open: boolean;
    chosen: readonly string[];
    colour: (group: string) => string;
    onToggle: (id: string, on: boolean) => void;
    onClose: () => void;
  } = $props();

  let dialog = $state<HTMLDialogElement | null>(null);
  let query = $state('');
  const uid = $props.id();
  const full = $derived(chosen.length >= MAX_LANES);

  $effect(() => {
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
  });

  const groups = $derived(
    GROUPS.map((g) => ({ ...g, lanes: LANES.filter((l) => l.group === g.id && laneMatches(l, query)) })).filter((g) => g.lanes.length > 0)
  );
</script>

<dialog bind:this={dialog} class="picker" aria-labelledby="{uid}-t" onclose={onClose} data-testid="bbl-picker">
  <header>
    <h3 id="{uid}-t">{BBL_TEXT.pickerTitle}</h3>
    <input type="search" bind:value={query} placeholder={BBL_TEXT.searchHint} aria-label={BBL_TEXT.searchLabel} />
    <p class="hint">{BBL_TEXT.pickerAvail(LANES.length)} {BBL_TEXT.pickerRule(MAX_LANES)}</p>
  </header>
  <div class="body">
    {#each groups as g (g.id)}
      {@const on = g.lanes.filter((l) => chosen.includes(l.id)).length}
      <details open={g.open || query !== '' || on > 0}>
        <summary><span class="dot" style:background={colour(g.id)}></span><b>{g.name}</b><span class="count">{BBL_TEXT.groupCount(on, g.lanes.length)}</span></summary>
        {#each g.lanes as l (l.id)}
          {@const isOn = chosen.includes(l.id)}
          {@const blocked = full && !isOn}
          <label class="fig" class:off={blocked}>
            <input type="checkbox" checked={isOn} disabled={blocked} onchange={(e) => onToggle(l.id, e.currentTarget.checked)} />
            <span>
              <span class="nm">{laneName(l.id)}</span>
              <span class="what">{laneWhat(l.id)}</span>
              <span class="wc">{BBL_TEXT.counterLabel(l.counter)}</span>
              {#if blocked}<span class="why">{BBL_TEXT.pickerFull(MAX_LANES)}</span>{/if}
            </span>
          </label>
        {/each}
      </details>
    {:else}
      <p class="empty">{BBL_TEXT.pickerNoMatch}</p>
    {/each}
  </div>
  <footer>
    <span class="hint">{BBL_TEXT.pickerFoot(chosen.length, MAX_LANES)}</span>
    <button type="button" class="primary-button" onclick={onClose}>{BBL_TEXT.pickerDone}</button>
  </footer>
</dialog>

<style>
  .picker { width: min(720px, calc(100vw - 32px)); max-height: min(80vh, 760px); padding: 0; border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); color: var(--text); }
  .picker[open] { display: grid; grid-template-rows: auto 1fr auto; }
  .picker::backdrop { background: var(--scrim); }
  header, footer { padding: var(--space-3) var(--space-4); }
  header { border-bottom: 1px solid var(--line); display: grid; gap: var(--space-2); }
  footer { border-top: 1px solid var(--line); display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
  h3 { margin: 0; }
  input[type='search'] { min-height: 44px; padding: 0 var(--space-3); border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface-2); color: var(--text); font: inherit; }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .body { overflow: auto; padding: var(--space-2) var(--space-4); }
  details { border-bottom: 1px solid var(--line); padding: var(--space-2) 0; }
  summary { display: flex; align-items: center; gap: var(--space-2); cursor: pointer; min-height: 36px; }
  .count { margin-left: auto; color: var(--text-2); font-size: var(--fs-3); }
  .dot { width: 10px; height: 10px; border-radius: 50%; flex: none; }
  .fig { display: grid; grid-template-columns: 20px 1fr; gap: var(--space-3); padding: var(--space-2) 0; align-items: start; }
  .fig.off { opacity: 0.6; }
  .fig span span { display: block; }
  .nm { font-weight: 600; }
  .what { color: var(--text-2); font-size: var(--fs-3); }
  .wc { color: var(--text-2); font-size: var(--fs-2); font-family: var(--mono, ui-monospace, monospace); overflow-wrap: anywhere; }
  .why { color: var(--warn); font-size: var(--fs-3); }
  .empty { color: var(--text-2); }
</style>

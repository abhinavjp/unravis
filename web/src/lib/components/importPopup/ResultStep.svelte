<script lang="ts">
  import { POPUP_TEXT } from '../../copy/importPopupCopy';

  // Step 3 of the Import files popup, result (wireframe, "Step 3 result"). It says how many files came in, lists them by kind, lists the
  // files that were left out and why, and names the computers that were added. The popup draws its body and its footer in two places,
  // so the parent renders this component twice: `part="body"` in the body snippet, `part="footer"` in the footer snippet.
  let {
    imported,
    notImported,
    computers,
    onOpenTimeline,
    onImportMore,
    onSeeComputers,
    part = 'body'
  }: {
    imported: { name: string; count: number }[];
    notImported: { name: string; reason: string }[];
    computers: string[];
    onOpenTimeline: () => void;
    onImportMore: () => void;
    /** Opens the computer list. The link is always drawn; without a handler it does nothing. */
    onSeeComputers?: () => void;
    part?: 'body' | 'footer';
  } = $props();

  const total = $derived(imported.reduce((sum, group) => sum + group.count, 0));
  // The copy reads "13 files imported"; the number is drawn larger, so the line is cut after its first word.
  const hero = $derived(POPUP_TEXT.resultHero(total));
  const heroCut = $derived(hero.indexOf(' '));
  const names = $derived(computers.join(', '));
  // The copy reads "Added 2 computers: iis_sandboxapi, sql-prod-2."; the names are drawn bold, so the lead-in is the copy with no names
  // (it ends in a full stop, which the names line adds back).
  const computersLead = $derived(POPUP_TEXT.computersLine(computers.length, '').slice(0, -1));
</script>

{#if part === 'body'}
  <div class="result-hero"><div class="num">{hero.slice(0, heroCut)} <small>{hero.slice(heroCut + 1)}</small></div></div>

  {#if imported.length > 0}
    <div class="result-section">
      <h4>{POPUP_TEXT.resultImported}</h4>
      {#each imported as group}
        <div class="result-line">
          <span class="rname">{group.name}</span>
          <span class="rcount">{group.count} {group.count === 1 ? 'file' : 'files'}</span>
        </div>
      {/each}
    </div>
  {/if}

  {#if notImported.length > 0}
    <div class="result-section">
      <h4>{POPUP_TEXT.resultNotImported}</h4>
      {#each notImported as file}
        <div class="result-line">
          <span class="rname">{file.name}</span>
          <span class="rcount">{file.reason || POPUP_TEXT.resultNoType}</span>
        </div>
      {/each}
    </div>
  {/if}

  {#if computers.length > 0}
    <div class="computers-line">
      {computersLead}<b>{names}</b>.
      <button type="button" class="linkbtn" onclick={() => onSeeComputers?.()}>{POPUP_TEXT.seeComputers}</button>
    </div>
  {/if}
{:else}
  <div class="foot-row">
    <span class="foot-spacer"></span>
    <button type="button" class="btn btn-secondary" onclick={() => onImportMore()}>{POPUP_TEXT.importMore}</button>
    <button type="button" class="btn btn-primary" data-focus="open-tl" onclick={() => onOpenTimeline()}>{POPUP_TEXT.openTimeline}</button>
  </div>
{/if}

<style>
  .foot-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .foot-spacer { flex: 1 1 auto; }

  .btn { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 36px; padding: 8px 14px; border-radius: var(--radius); font-size: 13px; font-weight: 600; cursor: pointer; transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease), color var(--dur) var(--ease); }
  .btn-primary { color: var(--accent-ink); background: var(--accent); border: 1px solid var(--accent); }
  .btn-primary:hover { background: color-mix(in srgb, var(--accent) 88%, var(--text)); }
  .btn-secondary { color: var(--text); background: transparent; border: 1px solid var(--line); }
  .btn-secondary:hover { border-color: var(--text-2); background: var(--surface-2); }

  .linkbtn { padding: 0; color: var(--accent); background: none; border: 0; font: inherit; font-size: 13px; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .linkbtn:hover { color: var(--text); }

  .result-hero { padding: 8px 0 16px; }
  .result-hero .num { font-size: 28px; font-weight: 600; letter-spacing: -.01em; font-family: var(--mono); }
  .result-hero .num small { font-size: 15px; color: var(--text-2); font-weight: 400; }
  .result-section { margin-top: 14px; }
  .result-section h4 { margin: 0 0 4px; font-family: var(--mono); font-size: 12px; font-weight: 500; color: var(--text-2); }
  .result-section h4::before { content: "// "; color: var(--text-2); font-weight: 400; }
  .result-line { display: flex; align-items: center; gap: 10px; padding: 8px 0; border-bottom: 1px solid var(--line-soft); font-size: 13px; }
  .result-line:last-child { border-bottom: 0; }
  .rname { min-width: 0; font-weight: 600; overflow-wrap: anywhere; }
  .rcount { margin-left: auto; padding-left: 12px; font-family: var(--mono); color: var(--text-2); font-size: 12px; text-align: right; }

  .computers-line { margin-top: 14px; padding: 10px 12px; background: var(--surface-2); border: 1px solid var(--line); border-radius: var(--radius); font-size: 13px; color: var(--text-2); }
  .computers-line b { color: var(--text); font-family: var(--mono); font-weight: 600; }

  @media (pointer: coarse), (max-width: 720px) {
    .btn { min-height: 44px; }
  }
</style>

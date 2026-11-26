<script lang="ts">
  import { tick } from 'svelte';
  import { POPUP_TEXT } from '../../copy/importPopupCopy';
  import { yearProblem } from './popupState.svelte';

  // The fix card for a log whose lines carry no year (Redis writes only the day and month). One card for each kind of log; it asks once for
  // the year and every file under it uses it. Open: type the year (offered from the file's last-saved date) and accept, or leave the files out.
  // Resolved: one line with the year, a quiet "Change the year". A year card never reads the files again, so there is no checking state.
  let {
    card, kind, year = '', suggestedYear, files, headFile, onResolve, onLeaveOut, onUndo
  }: {
    card: 'open' | 'resolved' | 'leftout';
    kind: string;
    /** The year given, once resolved. */
    year?: string;
    /** The year offered first. */
    suggestedYear: string;
    files: { key: string; name: string; size: string }[];
    /** The first file, read for the "What the file looks like" sample. */
    headFile?: File;
    onResolve?: (year: string) => void;
    onLeaveOut?: () => void;
    onUndo?: () => void;
  } = $props();

  const uid = $props.id();
  const yearId = uid + '-year';
  const hintId = uid + '-hint';
  const errId = uid + '-err';

  let typed = $state<string | null>(null);
  let touched = $state(false);
  let sample = $state<string[]>([]);
  let inputEl = $state<HTMLInputElement>();
  let primaryEl = $state<HTMLButtonElement>();
  let undoEl = $state<HTMLButtonElement>();

  const value = $derived((typed ?? suggestedYear).trim());
  const bad = $derived(touched && yearProblem(value));

  // The first few lines of the file, so the person sees what is missing. A file that cannot be read shows no sample.
  $effect(() => {
    const file = headFile;
    let live = true;
    sample = [];
    if (!file) return;
    void file.slice(0, 4096).text().then((text) => {
      if (live) sample = text.split(/\r?\n/).filter((line) => line.trim() !== '').slice(0, 4).map((line) => line.slice(0, 160));
    }).catch(() => {});
    return () => { live = false; };
  });

  /** Splits a line where the year would sit: after "26 Nov", before "13:47". Null when the line has no such place. */
  function gap(line: string): { pre: string; post: string } | null {
    const hit = /^(.*?\b[0-9]{1,2} [A-Z][a-z]{2}) ([0-9]{2}:[0-9]{2}.*)$/.exec(line);
    return hit ? { pre: hit[1], post: hit[2] } : null;
  }

  /** Puts the focus on the card's first control. Used by the shell's `data-focus` targets. */
  export async function focus(): Promise<void> {
    await tick();
    (card === 'open' ? (inputEl ?? primaryEl) : undoEl)?.focus();
  }

  function resolve() {
    touched = true;
    if (yearProblem(value)) { inputEl?.focus(); return; }
    onResolve?.(value);
    void tick().then(() => undoEl?.focus());
  }

  function change() {
    typed = year;
    touched = false;
    onUndo?.();
    void tick().then(() => inputEl?.focus());
  }
</script>

{#snippet warnIcon()}
  <svg class="ic" width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M8 2.5l6 10.5H2L8 2.5z" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round" /><path d="M8 7v3" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" /><circle cx="8" cy="11.4" r=".8" fill="currentColor" /></svg>
{/snippet}

{#snippet fileRows()}
  {#each files as file (file.key)}
    <div class="file"><span class="n">{file.name}</span><span class="s">{file.size}</span></div>
  {/each}
{/snippet}

{#snippet sampleBlock(done: boolean)}
  {#if sample.length > 0}
    <details class="sample" open={card === 'open' || undefined}>
      <summary><span class="chev" aria-hidden="true"></span>{POPUP_TEXT.yearSampleHead}{done ? '' : ' ' + POPUP_TEXT.yearSampleGap}</summary>
      <pre>{#each sample as line, index (index)}{@const parts = gap(line)}{#if parts}{parts.pre} {#if done}<span class="ok">{year}</span>{:else}<span class="yr">&nbsp;&nbsp;&nbsp;&nbsp;</span>{/if} {parts.post}{:else}{line}{/if}{index < sample.length - 1 ? '\n' : ''}{/each}</pre>
    </details>
  {/if}
{/snippet}

<div class="fix" class:resolved={card === 'resolved'} data-card={card}>
  {#if card === 'resolved'}
    <div class="fix-head">
      <div class="resolved-line">
        <svg class="ic" width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true"><circle cx="8" cy="8" r="6.5" stroke="currentColor" stroke-width="1.3" /><path d="M5.2 8.1l1.9 1.9 3.7-3.9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
        <span>{POPUP_TEXT.yearResolved(year, files.length)}</span>
      </div>
    </div>
    <div class="fix-body">
      <div class="actions"><button type="button" class="btn btn-quiet" bind:this={undoEl} onclick={change}>{POPUP_TEXT.yearChange}</button></div>
    </div>
    {@render sampleBlock(true)}
    {@render fileRows()}
  {:else if card === 'leftout'}
    <div class="fix-head">
      <div class="fix-title"><span class="muted">{@render warnIcon()}</span> {POPUP_TEXT.yearLeftOut(files.length, kind)}</div>
      <p class="fix-help">These files were not given a year. <button type="button" class="linkbtn" bind:this={undoEl} onclick={() => onUndo?.()}>{POPUP_TEXT.fixBringBack}</button></p>
    </div>
  {:else}
    <div class="fix-head">
      <div class="fix-title"><span class="warn">{@render warnIcon()}</span> {POPUP_TEXT.yearTitle(files.length, kind)}</div>
      <p class="fix-help">{POPUP_TEXT.yearHelp}</p>
    </div>
    <div class="fix-body">
      <div class="field">
        <label for={yearId}>{POPUP_TEXT.yearLabel}</label>
        <input type="text" id={yearId} inputmode="numeric" maxlength="4" bind:this={inputEl} value={typed ?? suggestedYear} aria-describedby={hintId + (bad ? ' ' + errId : '')} aria-invalid={bad} oninput={(event) => (typed = event.currentTarget.value)} onkeydown={(event) => { if (event.key === 'Enter') resolve(); }} />
        <span class="hint" id={hintId}>{POPUP_TEXT.yearHint}</span>
        {#if bad}<p class="fail" id={errId} role="alert">{POPUP_TEXT.yearProblem}</p>{/if}
      </div>
      <div class="actions">
        <button type="button" class="btn btn-primary" bind:this={primaryEl} onclick={resolve}>{POPUP_TEXT.yearAccept(value === '' ? suggestedYear : value, files.length)}</button>
        <button type="button" class="linkbtn" onclick={() => onLeaveOut?.()}>{POPUP_TEXT.fixLeaveOut(files.length)}</button>
      </div>
    </div>
    {@render sampleBlock(false)}
    {@render fileRows()}
  {/if}
</div>

<style>
  .fix { border: 1px solid var(--line); border-left: 3px solid var(--warn); border-radius: var(--radius); margin-bottom: 10px; background: color-mix(in srgb, var(--warn) 5%, var(--surface)); }
  .fix.resolved { border-left-color: var(--tone-ok); background: color-mix(in srgb, var(--tone-ok) 5%, var(--surface)); }
  .fix-head { padding: 12px 14px 4px; }
  .fix-title { font-size: 13px; font-weight: 600; display: flex; align-items: flex-start; gap: 8px; }
  .fix-title .warn { color: var(--warn); }
  .fix-title .muted { color: var(--text-2); }
  .fix-help { margin: 6px 0 0; color: var(--text-2); font-size: 12px; line-height: 1.5; }
  .fix-body { padding: 6px 14px 12px; display: grid; gap: 10px; }
  .actions { display: flex; flex-wrap: wrap; gap: 12px; align-items: center; }
  .resolved-line { display: flex; align-items: center; gap: 8px; color: var(--tone-ok); font-size: 13px; font-weight: 600; }
  .resolved-line svg { flex: 0 0 auto; }
  .fail { margin: 0; color: var(--bad); font-size: 12px; }

  .btn { display: inline-flex; align-items: center; justify-content: center; gap: 8px; border-radius: var(--radius); padding: 8px 14px; font-size: 13px; font-weight: 600; min-height: 33px; cursor: pointer; transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease), color var(--dur) var(--ease); }
  .btn-primary { color: var(--accent-ink); background: var(--accent); border: 1px solid var(--accent); }
  .btn-primary:hover { background: color-mix(in srgb, var(--accent) 88%, var(--text)); }
  .btn-quiet { color: var(--text-2); background: transparent; border: 1px solid transparent; padding: 6px 10px; font-size: 12px; font-weight: 500; }
  .btn-quiet:hover { color: var(--text); background: var(--surface-2); }
  .linkbtn { background: none; border: 0; color: var(--accent); font: inherit; font-size: 13px; padding: 0; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .linkbtn:hover { color: var(--text); }

  .field { display: grid; gap: 4px; max-width: 200px; }
  .field label { color: var(--text-2); font-size: 12px; font-weight: 500; }
  .field .hint { color: var(--text-2); font-size: 12px; }
  input[type='text'] { width: 100%; min-width: 0; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius); padding: 7px 9px; font: 13px var(--mono); letter-spacing: 0.04em; }
  input[type='text']:hover { border-color: var(--text-2); }
  input[aria-invalid='true'] { border-color: var(--bad); }

  .sample { border-top: 1px solid var(--line-soft); padding: 8px 14px 10px; }
  .sample summary { cursor: pointer; color: var(--text-2); font-size: 12px; list-style: none; display: flex; gap: 10px; align-items: center; min-height: 28px; }
  .sample summary::-webkit-details-marker { display: none; }
  .chev { flex: 0 0 auto; width: 8px; height: 8px; border-right: 1.5px solid var(--text-2); border-bottom: 1.5px solid var(--text-2); transform: rotate(-45deg); transition: transform var(--dur) var(--ease); }
  details[open] .chev { transform: rotate(45deg); }
  pre { margin: 8px 0 0; padding: 10px 12px; background: var(--bg); border: 1px solid var(--line); border-radius: var(--radius); font: 12px/1.6 var(--mono); overflow: auto; color: var(--text-2); }
  pre .yr { color: var(--warn); text-decoration: underline dotted; text-underline-offset: 3px; }
  pre .ok { color: var(--tone-ok); }
  .file { display: flex; gap: 10px; padding: 5px 14px; font: 12px var(--mono); color: var(--text-2); border-top: 1px solid var(--line-soft); }
  .file .n { color: var(--text); overflow-wrap: anywhere; min-width: 0; }
  .file .s { margin-left: auto; white-space: nowrap; }
  @media (prefers-reduced-motion: reduce) { .chev { transition: none; } .btn { transition: none; } }
</style>

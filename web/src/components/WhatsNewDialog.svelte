<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { UPD_TEXT as T } from '../lib/copy/updatesCopy';
  import { buildSlides, compareVersions, loadNotes, shortVersion, versionsSince, type ReleaseNotes } from '../lib/updateNotes';
  import { formatNoteDate } from '../lib/updatesPage';
  import { closeWhatsNew, whatsNew } from '../lib/updateState.svelte';

  // "What's new": a short tour of what changed. It has ONE fixed size, so it never resizes or jumps between slides: the picture area keeps its
  // place, a long caption scrolls inside its own area, and a picture is fitted inside the area, never cropped. Esc, X, Got it all close it and
  // remember the version, so it does not open again for the same update.
  let { current }: { current: string } = $props();

  let dialog = $state<HTMLDialogElement>();
  let notes = $state<ReleaseNotes[] | null>(null);
  let index = $state(0);
  let list = $state(false);
  let broken = $state<Record<string, boolean>>({});

  const covered = $derived(notes ? versionsSince(notes, whatsNew.seen, current) : []);
  const slides = $derived(buildSlides(covered));
  const slide = $derived(slides[Math.min(index, Math.max(0, slides.length - 1))]);
  const last = $derived(index >= slides.length - 1);
  const older = $derived(notes && covered.length ? notes.filter((r) => compareVersions(r.version, covered[covered.length - 1].version) < 0).length : 0);

  onMount(() => {
    dialog?.showModal();
    void tick().then(() => dialog?.querySelector<HTMLElement>('[data-primary]')?.focus());
    void loadNotes().then((all) => { notes = all; });
  });

  // Close the native dialog first so the browser hands focus back to whatever opened it, then let the app unmount us.
  function close() { dialog?.close(); closeWhatsNew(current); }
  function go(by: number) { index = Math.max(0, Math.min(slides.length - 1, index + by)); }
  function onKey(event: KeyboardEvent) {
    event.stopPropagation();
    if (event.key === 'Escape') { event.preventDefault(); close(); }
    else if (!list && (event.key === 'ArrowRight' || event.key === 'ArrowLeft') && !(event.target instanceof HTMLSelectElement)) { event.preventDefault(); go(event.key === 'ArrowRight' ? 1 : -1); }
  }
  const title = $derived(whatsNew.after ? T.tourTitleUpdated(shortVersion(current)) : T.tourTitleOpen(shortVersion(current)));
  const sub = $derived(covered.length > 1 ? T.tourSubSince(shortVersion(whatsNew.seen), covered.length) : T.tourSubOne);
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<dialog bind:this={dialog} class="wn" aria-labelledby="wn-title" onkeydown={onKey} oncancel={(event) => { event.preventDefault(); close(); }} data-testid="whatsnew">
  <header>
    <div><h2 id="wn-title">{title}</h2><p>{sub}</p></div>
    <button type="button" class="x" aria-label={T.tourClose} onclick={close}>×</button>
  </header>

  {#if notes === null}
    <div class="empty" role="status">{T.historyLoading}</div>
  {:else if !slides.length}
    <div class="empty">{T.tourEmpty}</div>
  {:else if list}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div class="listing" tabindex="0" role="region" aria-label={T.tourList} data-testid="whatsnew-list">
      {#each covered as release (release.version)}
        <section>
          <h3>{shortVersion(release.version)}{#if release.date}<span>{formatNoteDate(release.date)}</span>{/if}</h3>
          {#if release.headline}<p class="hl">{release.headline}</p>{/if}
          <ul>{#each release.highlights as item, n (n)}<li>{#if item.label}<b>{item.label}</b> {/if}{item.text}</li>{/each}</ul>
        </section>
      {/each}
      {#if older > 0}<p class="older">{T.tourOlder(older)}</p>{/if}
    </div>
  {:else if slide}
    <div class="meta"><span class="tag">{T.tourNew(shortVersion(slide.version))}</span>{#if slide.headline}<span class="head">{slide.headline}</span>{/if}<span class="count" aria-live="polite">{T.tourCount(index + 1, slides.length)}</span></div>
    <div class="stage" data-kind={slide.kind} data-testid="whatsnew-stage">
      {#if slide.kind === 'before-after' && slide.before && slide.image}
        <figure><figcaption>{T.before}</figcaption>{#if broken[slide.before]}<p class="gone">{T.imageMissing}</p>{:else}<img src={slide.before} alt={T.before + ': ' + (slide.alt ?? '')} onerror={() => { broken[slide.before!] = true; }} />{/if}</figure>
        <figure><figcaption>{T.after}</figcaption>{#if broken[slide.image]}<p class="gone">{T.imageMissing}</p>{:else}<img src={slide.image} alt={T.after + ': ' + (slide.alt ?? '')} onerror={() => { broken[slide.image!] = true; }} />{/if}</figure>
      {:else if slide.kind === 'screenshot' && slide.image}
        {#if broken[slide.image]}<p class="gone">{T.imageMissing}</p>{:else}<img src={slide.image} alt={slide.alt ?? ''} onerror={() => { broken[slide.image!] = true; }} />{/if}
      {:else}
        <div class="lines"><h3>{T.tourAlso(shortVersion(slide.version))}</h3>{#if slide.lines?.length}<ul>{#each slide.lines as line, n (n)}<li>{line}</li>{/each}</ul>{:else}<p>{slide.text}</p>{/if}</div>
      {/if}
    </div>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div class="caption" tabindex="0" role="group" aria-label={slide.label || T.tourNew(shortVersion(slide.version))}>{#if slide.kind !== 'note'}{#if slide.label}<b>{slide.label}</b> {/if}{slide.text}{/if}</div>
    <div class="progress" aria-hidden="true"><i style:width="{((index + 1) / slides.length) * 100}%"></i></div>
  {/if}

  <footer>
    {#if slides.length}<button type="button" class="link" onclick={() => { list = !list; }}>{list ? T.tourTour : T.tourList}</button>{:else}<span></span>{/if}
    <div class="acts">
      {#if slides.length && !list}
        <button type="button" class="secondary-button" disabled={index === 0} onclick={() => go(-1)}>{T.tourBack}</button>
        <!-- One persistent button: only its label and action change, so keyboard focus stays on it while the slides advance. -->
        <button type="button" class="primary-button" data-primary data-testid={last ? 'whatsnew-done' : 'whatsnew-next'} onclick={() => { if (last) close(); else go(1); }}>{last ? T.tourDone : T.tourNext}</button>
      {:else}
        <button type="button" class="primary-button" data-primary data-testid="whatsnew-done" onclick={close}>{T.tourDone}</button>
      {/if}
    </div>
  </footer>
</dialog>

<style>
  /* One size for every slide: nothing inside may change it. */
  .wn { width: min(920px, 94vw); height: min(680px, 90vh); padding: 0; color: var(--text); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); overflow: hidden; }
  .wn[open] { display: flex; flex-direction: column; }
  .wn::backdrop { background: color-mix(in srgb, var(--bg) 62%, transparent); }
  header { flex: 0 0 auto; display: flex; justify-content: space-between; gap: var(--space-3); padding: var(--space-4) var(--space-5, 22px) var(--space-3); border-bottom: 1px solid var(--hairline); }
  h2 { margin: 0; font-size: 19px; font-weight: 650; letter-spacing: -.01em; }
  header p { margin: 2px 0 0; color: var(--text-2); font-size: 13px; }
  .x { align-self: flex-start; width: 34px; height: 34px; font-size: 22px; line-height: 1; color: var(--text-2); border: 0; border-radius: var(--radius-sm); background: transparent; }
  .x:hover { background: var(--surface-2); color: var(--text); }
  .meta { flex: 0 0 auto; display: flex; align-items: center; gap: var(--space-3); padding: var(--space-3) var(--space-5, 22px) 0; }
  .tag { padding: 2px 10px; font-size: 12px; font-weight: 600; border: 1px solid var(--accent); border-radius: 999px; background: color-mix(in srgb, var(--accent) 10%, transparent); }
  .head { flex: 1 1 auto; min-width: 0; font-size: 13.5px; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .count { margin-left: auto; color: var(--text-2); font-size: 12.5px; font-variant-numeric: tabular-nums; }
  .stage { flex: 1 1 0; min-height: 0; display: flex; gap: var(--space-3); margin: var(--space-3) var(--space-5, 22px) 0; padding: var(--space-3); background: var(--surface-2); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .stage > img { flex: 1 1 0; min-width: 0; min-height: 0; width: 100%; height: 100%; object-fit: contain; }
  .stage figure { flex: 1 1 0; min-width: 0; min-height: 0; display: flex; flex-direction: column; gap: var(--space-2); margin: 0; }
  .stage figcaption { flex: 0 0 auto; color: var(--text-2); font-size: 12px; font-weight: 600; }
  .stage figure img { flex: 1 1 0; min-height: 0; width: 100%; object-fit: contain; }
  .gone { margin: auto; color: var(--text-2); font-size: 13px; }
  .lines { flex: 1 1 auto; min-width: 0; overflow-y: auto; padding: var(--space-1) var(--space-2); }
  .lines h3 { margin: 0 0 var(--space-2); font-size: 15px; }
  .lines ul, .listing ul { margin: 0; padding-left: 20px; font-size: 14px; line-height: 1.55; }
  .lines li, .listing li { margin: 4px 0; overflow-wrap: anywhere; }
  .caption { flex: 0 0 auto; height: 72px; overflow-y: auto; margin: var(--space-3) var(--space-5, 22px) 0; font-size: 14.5px; line-height: 1.55; overflow-wrap: anywhere; }
  .progress { flex: 0 0 auto; height: 3px; margin: var(--space-3) var(--space-5, 22px) 0; background: var(--hairline); border-radius: 2px; overflow: hidden; }
  .progress i { display: block; height: 100%; background: var(--accent); transition: width var(--dur) var(--ease); }
  .listing { flex: 1 1 0; min-height: 0; overflow-y: auto; padding: var(--space-3) var(--space-5, 22px); }
  .listing section { padding: var(--space-3) 0; border-top: 1px solid var(--hairline); }
  .listing section:first-child { border-top: 0; }
  .listing h3 { display: flex; gap: var(--space-3); align-items: baseline; margin: 0; font-size: 15px; }
  .listing h3 span { margin-left: auto; color: var(--text-2); font-size: 12.5px; font-weight: 400; }
  .hl { margin: 2px 0 var(--space-2); font-size: 13.5px; font-weight: 600; }
  .older { color: var(--text-2); font-size: 13px; }
  .empty { flex: 1 1 0; display: grid; place-items: center; padding: var(--space-5, 22px); color: var(--text-2); font-size: 14px; text-align: center; }
  footer { flex: 0 0 auto; display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); padding: var(--space-3) var(--space-5, 22px) var(--space-4); }
  .acts { display: flex; gap: var(--space-2); }
  .link { padding: 0; border: 0; background: none; color: var(--accent); font-size: 13px; font-weight: 500; text-decoration: underline; }
  .caption:focus-visible, .listing:focus-visible, .link:focus-visible, .x:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  @media (max-width: 720px) { .stage { flex-direction: column; } .caption { height: 96px; } }
</style>

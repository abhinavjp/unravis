<script lang="ts">
  import type { StripModel } from '../lib/zoom';
  import { zoomCopy } from '../lib/copy/zoomCopy';

  // A compact strip above the time charts. It only draws the model it is given (lib/zoom.ts stripModel) and reports button presses.
  // It starts as one line, "Zoom: 7 days", and opens when used.
  let { model, open = $bindable(false), announcement = '', onBack, onEarlier, onLater, onZoomIn, onZoomOut, onReset, onJump }: {
    model: StripModel; open?: boolean; announcement?: string;
    onBack: () => void; onEarlier: () => void; onLater: () => void; onZoomIn: () => void; onZoomOut: () => void; onReset: () => void; onJump: (index: number) => void;
  } = $props();
  const bars = $derived(model.overview.bars);
  const step = $derived(bars.length ? 100 / bars.length : 0);
</script>

<section class="zoom-strip" aria-label={zoomCopy.heading}>
  <details bind:open>
    <summary title={zoomCopy.expand}><span class="summary-text">{model.summary}</span>{#if model.notice}<span class="summary-notice">{model.notice}</span>{/if}</summary>
    <div class="zoom-body">
      <div class="zoom-row">
        <nav aria-label={zoomCopy.breadcrumbLabel}>
          <ol class="crumbs">
            {#each model.crumbs as crumb (crumb.index)}
              <li>{#if crumb.current}<span class="crumb-current" aria-current="step">{crumb.label}</span>{:else}<button type="button" class="crumb-link" onclick={() => onJump(crumb.index)}>{crumb.label}</button>{/if}</li>
            {/each}
          </ol>
        </nav>
        <div class="zoom-buttons">
          <button type="button" class="quiet-button" disabled={!model.canBack} onclick={onBack}>{zoomCopy.back}</button>
          <button type="button" class="quiet-button" disabled={!model.canEarlier} onclick={onEarlier}>{zoomCopy.earlier}</button>
          <button type="button" class="quiet-button" disabled={!model.canLater} onclick={onLater}>{zoomCopy.later}</button>
          <button type="button" class="quiet-button" disabled={!model.canIn} onclick={onZoomIn}>{zoomCopy.zoomIn}</button>
          <button type="button" class="quiet-button" disabled={!model.canOut} onclick={onZoomOut}>{zoomCopy.zoomOut}</button>
          <button type="button" class="quiet-button" disabled={!model.canReset} onclick={onReset}>{zoomCopy.reset}</button>
        </div>
      </div>
      {#if model.overview.hasData}
        <svg class="overview" viewBox="0 0 100 14" preserveAspectRatio="none" role="img" aria-label={model.overview.alt}>
          {#each bars as value, index (index)}<rect class="bar" x={index * step} y={14 - value * 12} width={Math.max(step - 0.3, 0.2)} height={value * 12} />{/each}
          <rect class="mark" x={model.overview.left * 100} y="0" width={Math.max(model.overview.width * 100, 0.6)} height="14" />
        </svg>
      {:else}
        <p class="zoom-note overview-empty">{zoomCopy.overviewEmpty}</p>
      {/if}
      <p class="zoom-line"><strong class="level">{model.levelName}</strong> {model.rangeLine} {model.intervalLine}</p>
      {#if model.message}<p class="zoom-message" data-tone={model.messageTone}>{model.message}</p>{/if}
      {#if model.notice}<p class="zoom-message" data-tone="info">{model.notice}</p>{/if}
      {#if model.asOfLine}<p class="zoom-note">{model.asOfLine}</p>{/if}
      {#if model.scopeLine}<p class="zoom-note">{model.scopeLine}</p>{/if}
      {#if model.referenceLine}<p class="zoom-note">{model.referenceLine}</p>{/if}
      <p class="zoom-note">{zoomCopy.howTo}</p>
    </div>
  </details>
  <p class="sr-only" role="status" aria-live="polite">{model.rangeLine} {model.message} {model.notice} {announcement}</p>
</section>

<style>
  .zoom-strip { margin: 0 0 var(--space-3, 12px); border-top: var(--hairline-width, 1px) solid var(--hairline, var(--line)); border-bottom: var(--hairline-width, 1px) solid var(--hairline, var(--line)); font-size: 13px; }
  summary { display: flex; align-items: center; gap: 6px; padding: 6px 0; cursor: pointer; color: var(--text-2); list-style: none; font-weight: 600; }
  summary::-webkit-details-marker { display: none; }
  summary:hover { color: var(--text); }
  .summary-notice { font-weight: 400; color: var(--text); }
  .summary-notice::before { content: '·'; margin-right: 6px; color: var(--text-2); }
  /* The arrow is decoration: it only shows that the line opens. */
  summary::after { content: '▸'; display: inline-block; transition: transform var(--dur, 120ms) var(--ease, ease); }
  details[open] summary::after { transform: rotate(90deg); }
  .zoom-body { display: grid; gap: 8px; padding: 4px 0 10px; }
  .zoom-row { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px 16px; }
  .crumbs { display: flex; flex-wrap: wrap; align-items: center; gap: 4px; margin: 0; padding: 0; list-style: none; }
  .crumbs li + li::before { content: '›'; margin-right: 4px; color: var(--text-2); }
  .crumb-link { padding: 0; border: 0; background: none; color: var(--accent); cursor: pointer; font: inherit; text-decoration: underline; }
  .crumb-current { font-weight: 600; color: var(--text); }
  .zoom-buttons { display: flex; flex-wrap: wrap; gap: 4px; }
  .zoom-buttons button:disabled { opacity: .45; cursor: not-allowed; }
  .overview { width: 100%; height: 28px; display: block; background: var(--surface-2); border-radius: 2px; }
  .overview .bar { fill: var(--text-2); opacity: .55; }
  .overview .mark { fill: var(--accent); opacity: .3; stroke: var(--accent); stroke-width: .4; vector-effect: non-scaling-stroke; }
  .zoom-line, .zoom-note, .zoom-message { margin: 0; }
  .zoom-note { color: var(--text-2); font-size: 12px; }
  .zoom-message[data-tone='warn'] { color: var(--text); font-weight: 600; }
  .zoom-message[data-tone='error'] { color: var(--bad); font-weight: 600; }
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
</style>

<script lang="ts">
  import { tick } from 'svelte';

  let { steps, current, maxSeen, disabled, onGo }: {
    steps: { id: string; label: string; skipped?: boolean }[];
    current: number;
    maxSeen: number;
    disabled: boolean;
    onGo: (index: number) => void;
  } = $props();

  let rail = $state<HTMLOListElement>();

  // Keep the current step in view when the rail is wider than its container.
  $effect(() => {
    if (!steps[current]) return;
    let cancelled = false;
    void tick().then(() => {
      if (cancelled || !rail) return;
      const active = rail.querySelector<HTMLElement>('[aria-current="step"]');
      if (!active) return;
      const railBounds = rail.getBoundingClientRect();
      const activeBounds = active.getBoundingClientRect();
      if (activeBounds.left < railBounds.left) {
        rail.scrollLeft -= railBounds.left - activeBounds.left;
      } else if (activeBounds.right > railBounds.right) {
        rail.scrollLeft += activeBounds.right - railBounds.right;
      }
    });
    return () => { cancelled = true; };
  });
</script>

<div class="progress">
  <ol class="rail" role="list" aria-label={`Progress, step ${current + 1} of ${steps.length}`} bind:this={rail}>
    {#each steps as step, index (step.id)}
      <li class:past={index < current && !step.skipped} class:skipped={step.skipped} class:current={index === current}>
        <button
          type="button"
          disabled={disabled || step.skipped || index > maxSeen}
          aria-current={index === current ? 'step' : undefined}
          aria-label={`${step.label}, step ${index + 1}${step.skipped ? ', skipped' : ''}`}
          title={step.skipped ? 'Not needed for this choice' : undefined}
          onclick={() => onGo(index)}
        >
          <span class="dot" aria-hidden="true">{#if index < current && !step.skipped}✓{:else}{index + 1}{/if}</span>
          <span class="label">{step.label}</span>
        </button>
      </li>
    {/each}
  </ol>
  <div class="mobile-label" aria-hidden="true">{steps[current]?.label}</div>
</div>

<style>
  .rail {
    display: flex;
    margin: var(--space-4) 0 0;
    padding: 0;
    list-style: none;
    overflow-x: auto;
  }
  .rail li {
    flex: 1;
    position: relative;
    min-width: 0;
    text-align: center;
    font-size: var(--fs-2);
    color: var(--text-2);
  }
  .rail li::before {
    content: '';
    position: absolute;
    top: 11px;
    left: -50%;
    width: 100%;
    height: 2px;
    background: var(--line);
    z-index: 0;
  }
  .rail li:first-child::before { display: none; }
  .rail li::after {
    content: '';
    position: absolute;
    top: 11px;
    left: -50%;
    width: 0;
    height: 2px;
    background: var(--accent);
    transition: width 360ms var(--ease);
    z-index: 0;
  }
  .rail li.past::after, .rail li.current::after { width: 100%; }
  .rail li:first-child::after { display: none; }
  .rail button {
    position: relative;
    z-index: 1; /* above the next step's connector line, which reaches back to this circle */
    display: grid;
    justify-items: center;
    gap: var(--space-1);
    box-sizing: border-box;
    width: 100%;
    padding: 0 var(--space-1);
    border: 0;
    background: transparent;
    color: inherit;
    font: inherit;
    cursor: pointer;
  }
  .rail button:disabled { cursor: default; opacity: 1; }
  .rail button:focus-visible { outline: 2px solid var(--focus); outline-offset: 3px; }
  .dot {
    display: grid;
    place-items: center;
    box-sizing: border-box;
    width: 24px;
    height: 24px;
    border: 2px solid var(--line);
    border-radius: 50%;
    background: var(--surface);
    font-size: var(--fs-2);
    font-weight: 600;
    transition: all var(--dur) var(--ease);
  }
  .past .dot { border-color: var(--accent); background: var(--accent); color: var(--accent-ink); }
  .current .dot { border-color: var(--accent); color: var(--accent); box-shadow: 0 0 0 4px color-mix(in srgb, var(--accent) 12%, transparent); }
  .skipped .dot { border-style: dashed; color: var(--text-2); }
  .skipped button { color: var(--text-2); }
  .current button { color: var(--text); font-weight: 600; }
  .label { overflow-wrap: anywhere; }
  .mobile-label { display: none; }

  @media (max-width: 640px) {
    .rail .label { display: none; }
    .rail .current .label { display: block; }
    .rail button { min-height: 40px; }
  }
</style>

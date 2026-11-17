<script lang="ts">
  import { tick } from 'svelte';

  let { steps, current, maxSeen, disabled, onGo }: {
    steps: { id: string; label: string }[];
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
      <li class:past={index < current} class:current={index === current}>
        <button
          type="button"
          disabled={disabled || index > maxSeen}
          aria-current={index === current ? 'step' : undefined}
          aria-label={`${step.label}, step ${index + 1}`}
          onclick={() => onGo(index)}
        >
          <span class="dot" aria-hidden="true">{index + 1}</span>
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
    margin: 0;
    padding: 0;
    list-style: none;
    overflow-x: auto;
  }
  .rail li {
    position: relative;
    flex: 1 0 40px;
    min-width: 0;
    text-align: center;
  }
  .rail li:not(:first-child)::before {
    content: '';
    position: absolute;
    top: 19px;
    left: -50%;
    width: 100%;
    height: 2px;
    background: var(--control-line);
  }
  .rail li.past::before, .rail li.current::before { background: var(--accent); }
  .rail button {
    position: relative;
    z-index: 1; /* above the next step's connector line, which reaches back to this circle */
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    box-sizing: border-box;
    width: 100%;
    min-width: 40px;
    min-height: 40px;
    padding: 8px 2px;
    border: 0;
    background: transparent;
    color: var(--text-2);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
  }
  .rail button:disabled { cursor: default; opacity: 1; }
  .rail button:focus-visible { outline: 2px solid var(--focus); outline-offset: -2px; }
  .dot {
    display: grid;
    place-items: center;
    box-sizing: border-box;
    width: 24px;
    height: 24px;
    border: 2px solid var(--control-line);
    border-radius: 50%;
    background: var(--surface);
    font-weight: 600;
  }
  .past .dot { border-color: var(--accent); background: var(--accent); color: var(--accent-ink); }
  .current .dot { border-color: var(--accent); color: var(--accent); }
  .current button { color: var(--text); font-weight: 600; }
  .label { overflow-wrap: anywhere; }
  .mobile-label { display: none; }

  @media (max-width: 640px) {
    .rail .label { display: none; }
    /* Eight steps (the SQL step adds one) must fit a phone without a sideways scroll. */
    .rail li { flex: 1 0 34px; }
    .rail button { min-width: 34px; }
    .mobile-label {
      display: block;
      margin-top: 4px;
      color: var(--text);
      font-size: 12px;
      font-weight: 600;
      text-align: center;
    }
  }
</style>

<script lang="ts">
  import { TIME_ZONE_OPTIONS, timeZone, type TimeZoneChoice } from '../lib/timeZone.svelte';
  // One choice for every timestamp, chart axis and time picker: London by default, or Dublin, UTC, India, Australia (Sydney) or this
  // computer's own zone. Mount it once, in the header.
</script>

<label class="zone-toggle" title="Every time on screen, and every time you type, is in this zone">
  <span class="zone-word">Times in</span>
  <select aria-label="Show and type times in" value={timeZone.choice} onchange={(e) => timeZone.set(e.currentTarget.value as TimeZoneChoice)}>
    {#each TIME_ZONE_OPTIONS as option (option.id)}
      <option value={option.id}>{option.id === 'local' ? `${option.label} (${timeZone.browserZone})` : option.label}</option>
    {/each}
  </select>
</label>

<style>
  .zone-toggle { display: inline-flex; flex-shrink: 0; align-items: center; gap: var(--space-2); color: var(--text-2); font-size: var(--fs-2); white-space: nowrap; }
  .zone-toggle select { width: auto; min-height: 28px; padding: var(--space-1) var(--space-2); font: inherit; font-size: var(--fs-2); font-weight: 500; color: var(--text); }
  .zone-toggle select:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  @media (max-width: 1100px) { .zone-word { display: none; } }
  /* On a phone the box is only as wide as the short names; the open list still shows every name in full. */
  @media (max-width: 640px) { .zone-toggle select { max-width: 96px; text-overflow: ellipsis; } }
  /* Touch targets on a phone, a tablet and any touch screen: 40 px high, never under 32. */
  @media (max-width: 1024px), (pointer: coarse) { .zone-toggle select { min-height: 40px; } }
</style>

<script lang="ts">
  import type { Crumb } from '../lib/depth';
  // The trail beyond D0. Every earlier level is a button that steps back to it; the last is the current place.
  let { crumbs, onNavigate }: { crumbs: Crumb[]; onNavigate: (crumb: Crumb) => void } = $props();
</script>

{#if crumbs.length}
  <nav class="crumbs" aria-label="Breadcrumb">
    <ol>
      {#each crumbs as crumb (crumb.id)}
        <li>
          {#if crumb.current}<span aria-current="page" title={crumb.label}>{crumb.label}</span>
          {:else}<button type="button" title={crumb.label} onclick={() => onNavigate(crumb)}>{crumb.label}</button>{/if}
        </li>
      {/each}
    </ol>
  </nav>
{:else}
  <div class="crumbs"></div>
{/if}

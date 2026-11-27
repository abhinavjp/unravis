<script lang="ts">
  import type { Snippet } from 'svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  let { back, backLabel = T.back, summary = '', primary, extra, reason = '' }: {
    back?: () => void; backLabel?: string; summary?: string | Snippet;
    primary: { label: string; onclick?: () => void; disabled?: boolean; busyLabel?: string; form?: string };
    extra?: Snippet; reason?: string;
  } = $props();
</script>
<footer class="foot">
  {#if back}<button type="button" class="secondary-button" onclick={back}>{backLabel}</button>{/if}
  <div class="summary">{#if typeof summary === 'string'}{summary}{:else}{@render summary()}{/if}{#if primary.disabled && reason}<span>{reason}</span>{/if}</div>
  {#if extra}{@render extra()}{/if}
  <button type={primary.form ? 'submit' : 'button'} form={primary.form} class="primary-button" disabled={primary.disabled} onclick={primary.onclick}>{primary.busyLabel || primary.label}</button>
</footer>
<style>
  .foot { position:sticky; bottom:0; z-index:2; display:flex; flex-wrap:wrap; align-items:center; gap:12px; padding:14px 0; border-top:1px solid var(--line); background:var(--bg); }
  .summary { flex:1 1 180px; color:var(--text-2); font-size:var(--fs-3); }
  .summary span { display:block; }
</style>

<script lang="ts">
  // The small line icons of the code-mode popup and badge (the approved prototype's own set). Decorative: the words beside them say the same.
  let { name, size = 16 }: { name: 'x' | 'term' | 'lock' | 'check' | 'box' | 'eye' | 'alert'; size?: number } = $props();
</script>

<svg class="code-icon" width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">
  {#if name === 'x'}<path d="M3.5 3.5l9 9M12.5 3.5l-9 9" />
  {:else if name === 'term'}<rect x="1.75" y="2.75" width="12.5" height="10.5" rx="1" /><path d="M4.5 6.25L6.75 8 4.5 9.75M8.25 10h3" />
  {:else if name === 'lock'}<rect x="3.25" y="7" width="9.5" height="6.75" rx="1" /><path d="M5.25 7V5.25a2.75 2.75 0 015.5 0V7" />
  {:else if name === 'check'}<path d="M3.25 8.5l3 3 6.5-7" />
  {:else if name === 'box'}<path d="M8 1.75l5.75 3.1v6.3L8 14.25l-5.75-3.1v-6.3L8 1.75zM2.5 5l5.5 3 5.5-3M8 8v6" />
  {:else if name === 'eye'}<path d="M1.5 8S4 3.5 8 3.5 14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z" /><circle cx="8" cy="8" r="2" />
  {:else}<path d="M8 2.25l6.25 11H1.75l6.25-11zM8 6.5v3M8 11.5v.01" />{/if}
</svg>

<style>
  .code-icon { flex: none; }
</style>

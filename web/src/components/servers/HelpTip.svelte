<script lang="ts">
  let { id, label, text }: { id: string; label: string; text: string } = $props();
  let open = $state(false);
</script>
<button type="button" class="help" aria-label={label} aria-expanded={open} aria-controls={id} onclick={() => open = !open}>?</button>
<small id={id} hidden={!open}>{text}</small>
<style>
  .help { position:absolute; top:-1px; right:0; display:inline-grid; place-items:center; width:20px; height:20px; font-size:12px; line-height:1; padding:0; border:1px solid var(--control-line); border-radius:50%; background:var(--surface); color:var(--text-2); }
  small { display:block; color:var(--text-2); }
  small[hidden] { display:none; }
</style>

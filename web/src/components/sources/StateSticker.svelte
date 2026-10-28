<script lang="ts">
  import './sources.css';

  // One sticker for a state word. Information, not a button. The words are fixed: Ready, Partly ready, Cannot test yet, Failed for a
  // source, and Works, Failed, Not tested for one connection test. Anything else is shown as Cannot test yet, never as a pass.
  let { word }: { word: string } = $props();

  const KNOWN: Record<string, string> = {
    Ready: 'ready', 'Partly ready': 'partly', 'Cannot test yet': 'cannot', Failed: 'failed', Works: 'works', 'Not tested': 'untested'
  };
  const shown = $derived(word in KNOWN ? word : 'Cannot test yet');
</script>

<span class="src-sticker {KNOWN[shown]}" data-word={shown}>{shown}</span>

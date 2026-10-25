<script lang="ts">
  import FieldHelp from './FieldHelp.svelte';
  // `help` is an optional help key; the "i" button sits under the inputs, outside the legend so the group name stays "From" or "To".
  let {
    id,
    label,
    value,
    onChange,
    help
  }: { id: string; label: string; value: string; onChange: (datePart: string, timePart: string) => void; help?: string } = $props();
</script>

<fieldset class="date-time-field">
  <legend>{label}</legend>
  <div class="date-time-controls">
    <div class="date-time-control"><label for={`${id}-date`}>Date</label><input id={`${id}-date`} type="date" value={value.slice(0, 10)} onchange={(event) => onChange(event.currentTarget.value, value.slice(11, 23))} /></div>
    <div class="date-time-control"><label for={`${id}-time`}>Time</label><input id={`${id}-time`} type="time" step="0.001" value={value.slice(11, 23)} onchange={(event) => onChange(value.slice(0, 10), event.currentTarget.value)} /></div>
  </div>
  {#if help}<div class="label-line date-time-help"><FieldHelp id={help} /></div>{/if}
</fieldset>

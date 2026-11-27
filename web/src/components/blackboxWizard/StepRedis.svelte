<script lang="ts">
  // The Redis step of the Place a Blackbox pop-up (B4 settings). Every choice is written to settings.redis by
  // buildSettings; the password is never here (it is typed on the PC that runs Redis, with set-redis-password).
  import SegmentedControl from './SegmentedControl.svelte';
  import {
    redisPresetNow, redisRowOn, setRedisProfile, setRedisRow,
    type RedisProfile, type RedisRowId, type WizardState
  } from '../../lib/blackboxWizard';
  import { BBW_TEXT as W } from '../../lib/copy/blackboxWizardCopy';

  let { wizard = $bindable() }: { wizard: WizardState } = $props();

  const now = $derived(redisPresetNow(wizard.redisChoice));
  const start = $derived<RedisProfile | 'custom'>(now);
  const options = $derived([
    { value: 'standard' as const, label: W['redis.standard'] },
    { value: 'everything' as const, label: W['redis.everything'] },
    { value: 'custom' as const, label: W['redis.custom'], disabled: true }
  ]);
  const changes = $derived(
    (['slowlog', 'latency', 'clients', 'slowArgs'] as RedisRowId[]).filter((r) => wizard.redisChoice.rows[r] !== undefined).length
  );

  const rows: { id: RedisRowId; title: string; hint: string; tag?: string; warn?: boolean }[] = [
    { id: 'slowlog', title: W['redis.slowlogTitle'], hint: W['redis.slowlogHint'] },
    { id: 'latency', title: W['redis.latencyTitle'], hint: W['redis.latencyHint'] },
    { id: 'clients', title: W['redis.clientsTitle'], hint: W['redis.clientsHint'] },
    { id: 'slowArgs', title: W['redis.slowArgsTitle'], hint: W['redis.slowArgsHint'], tag: W['redis.tagOff'], warn: true }
  ];

  function pick(value: RedisProfile | 'custom' | null): void {
    if (value === 'standard' || value === 'everything') wizard = setRedisProfile(wizard, value);
  }
</script>

<div class="redis-step">
  {#if wizard.where === 'pack'}
    <p class="note">{W['redis.packNote']}</p>
  {:else}
    <p class="note ok">{W['redis.found']}</p>
  {/if}

  <SegmentedControl legend={W['redis.startHeading']} {options} bind:value={() => start, pick} />
  {#if now === 'custom'}
    <p class="hint">{W['redis.customLine'](changes)}</p>
  {/if}

  <section class="list">
    <h3>{W['redis.rowsHeading']}</h3>
    <div class="row locked">
      <span class="mark" aria-hidden="true">✓</span>
      <span class="copy"><span class="title-line"><b>{W['redis.serverTitle']}</b><span class="tag">{W['redis.tagAlways']}</span></span><span class="hint">{W['redis.serverHint']}</span></span>
    </div>
    {#each rows as row (row.id)}
      <label class="row">
        <input type="checkbox" checked={redisRowOn(wizard.redisChoice, row.id)} onchange={(e) => { wizard = setRedisRow(wizard, row.id, e.currentTarget.checked); }} />
        <span class="copy"><span class="title-line"><b>{row.title}</b>{#if row.tag}<span class="tag" class:warn={row.warn}>{row.tag}</span>{/if}</span><span class="hint">{row.hint}</span></span>
      </label>
    {/each}
  </section>

  {#if wizard.where === 'pc'}<p class="hint">{W['redis.passwordNote']}</p>{/if}
  <p class="hint">{W['redis.localOnly']}</p>
</div>

<style>
  .redis-step { display: grid; gap: var(--space-3); min-width: 0; }
  p { margin: 0; color: var(--text-2); }
  .note { border: 1px solid var(--line); border-radius: var(--radius-sm); padding: var(--space-2) var(--space-3); background: var(--surface-2); }
  .note.ok { border-color: var(--setup-ok); }
  .list { border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); }
  h3 { margin: 0; padding: var(--space-2) var(--space-3); font-size: var(--fs-3); color: var(--text-2); }
  .row { display: grid; grid-template-columns: 20px 1fr; gap: var(--space-3); align-items: start; padding: var(--space-2) var(--space-3); border-top: 1px solid var(--line); }
  .copy { display: grid; gap: 2px; }
  .hint { color: var(--text-2); font-size: var(--fs-3); }
  .title-line { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-2); }
  .tag { padding: 1px var(--space-2); border: 1px solid var(--line); border-radius: var(--radius-sm); color: var(--text-2); font-size: var(--fs-2); white-space: nowrap; }
  .tag.warn { border-color: var(--setup-warn); }
  .mark { color: var(--setup-ok); }
</style>

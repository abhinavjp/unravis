<script lang="ts">
  import { CaseApiError, HOST_ROLES, getHostEntries, putHostEntries, type HostEntry } from '../cases';
  import { IMPORT_TEXT, errorText, hostRoleLabel } from '../copy/importCopy';

  // The host list: one row per server. Saving replaces the whole list on the server, which checks it and says what is wrong
  // by rule, never by value. A file that names a host that is not here is refused at import.
  let { onSaved }: { onSaved?: (labels: string[]) => void } = $props();

  type Row = { key: number; label: string; aliases: string; role: string; tz: string; cores: string; runtime: string };

  let open = $state(false);
  let loading = $state(false);
  let saving = $state(false);
  let loaded = $state(false);
  let rows = $state<Row[]>([]);
  let problem = $state('');
  let saved = $state(false);
  let nextKey = 1;
  let controller: AbortController | null = null;

  const toRow = (e: HostEntry): Row => ({ key: nextKey++, label: e.label, aliases: e.aliases.join(', '), role: e.role, tz: e.tz, cores: e.logicalCores === null ? '' : String(e.logicalCores), runtime: e.runtime });
  const blank = (): Row => ({ key: nextKey++, label: '', aliases: '', role: '', tz: '', cores: '', runtime: '' });

  /** Null when the cores box is blank, NaN when it is not a whole number in range. */
  function coresOf(text: string): number | null {
    const t = text.trim();
    if (t === '') return null;
    const n = Number(t);
    return Number.isInteger(n) && n >= 1 && n <= 1024 ? n : Number.NaN;
  }

  function fail(error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') return;
    problem = error instanceof CaseApiError ? errorText(error) : errorText({ status: 0, reason: 'unreachable', message: '' });
  }

  async function toggle() {
    open = !open;
    problem = ''; saved = false;
    if (!open || loaded) return;
    controller?.abort();
    controller = new AbortController();
    loading = true;
    try {
      rows = (await getHostEntries(controller.signal)).map(toRow);
      loaded = true;
    } catch (error) { fail(error); open = true; } finally { loading = false; }
  }

  $effect(() => () => controller?.abort());

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    saved = false;
    if (rows.some((r) => !r.label.trim())) { problem = IMPORT_TEXT.hostLabelMissing; return; }
    if (rows.some((r) => Number.isNaN(coresOf(r.cores)))) { problem = IMPORT_TEXT.hostCoresBad; return; }
    const entries: HostEntry[] = rows.map((r) => ({
      label: r.label, aliases: r.aliases.split(',').map((a) => a.trim()).filter(Boolean), role: r.role, tz: r.tz, logicalCores: coresOf(r.cores), runtime: r.runtime
    }));
    problem = '';
    saving = true;
    controller?.abort();
    controller = new AbortController();
    try {
      const kept = await putHostEntries(entries, controller.signal);
      rows = kept.map(toRow);
      saved = true;
      onSaved?.(kept.map((h) => h.label).filter(Boolean).sort((a, b) => a.localeCompare(b)));
    } catch (error) { fail(error); } finally { saving = false; }
  }

  const edit = () => { saved = false; };
</script>

<section class="hosts" aria-labelledby="hosts-heading">
  <h3 id="hosts-heading">{IMPORT_TEXT.hostsHeading}</h3>
  <p class="note">{IMPORT_TEXT.hostsIntro}</p>
  <div><button type="button" class="secondary-button" aria-expanded={open} aria-controls="hosts-form" onclick={toggle}>{open ? IMPORT_TEXT.hostsClose : IMPORT_TEXT.hostsOpen}</button></div>

  {#if open}
    <form id="hosts-form" onsubmit={save} oninput={edit}>
      {#if loading}
        <p class="note" role="status">{IMPORT_TEXT.hostsLoading}</p>
      {:else}
        {#if rows.length === 0}<p class="note">{IMPORT_TEXT.hostsNone}</p>{/if}
        <ul class="rows">
          {#each rows as row, i (row.key)}
            <li>
              <div class="grid">
                <label>{IMPORT_TEXT.hostFields.label}<input type="text" bind:value={row.label} maxlength="253" autocomplete="off" spellcheck="false" /></label>
                <label>{IMPORT_TEXT.hostFields.aliases}<input type="text" bind:value={row.aliases} autocomplete="off" spellcheck="false" /></label>
                <label>{IMPORT_TEXT.hostFields.role}
                  <select bind:value={row.role}>
                    <option value="">{IMPORT_TEXT.hostRoleNone}</option>
                    {#each HOST_ROLES as role (role)}<option value={role}>{hostRoleLabel(role)}</option>{/each}
                  </select>
                </label>
                <label>{IMPORT_TEXT.hostFields.tz}<input type="text" bind:value={row.tz} autocomplete="off" spellcheck="false" aria-describedby="hosts-tz-note" /></label>
                <label>{IMPORT_TEXT.hostFields.cores}<input type="text" inputmode="numeric" bind:value={row.cores} autocomplete="off" /></label>
                <label>{IMPORT_TEXT.hostFields.runtime}<input type="text" bind:value={row.runtime} autocomplete="off" spellcheck="false" /></label>
              </div>
              <button type="button" class="secondary-button remove" aria-label={IMPORT_TEXT.hostRemove + ': ' + (row.label || String(i + 1))} onclick={() => { rows = rows.filter((r) => r.key !== row.key); saved = false; }}>{IMPORT_TEXT.hostRemove}</button>
            </li>
          {/each}
        </ul>
        <p class="note" id="hosts-tz-note">{IMPORT_TEXT.hostTzNote}</p>
        <div class="actions">
          <button type="button" class="secondary-button" onclick={() => { rows = [...rows, blank()]; saved = false; }}>{IMPORT_TEXT.hostAdd}</button>
          <button type="submit" class="primary-button" disabled={saving}>{saving ? IMPORT_TEXT.hostSaving : IMPORT_TEXT.hostSave}</button>
        </div>
      {/if}
    </form>
  {/if}

  <div aria-live="polite">
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
    {#if saved}<p class="done" role="status">{IMPORT_TEXT.hostSaved}</p>{/if}
  </div>
</section>

<style>
  .hosts { display: grid; gap: var(--space-2); min-width: 0; padding-top: var(--space-4); border-top: 1px solid var(--hairline); }
  h3 { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  .note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  form { display: grid; gap: var(--space-3); min-width: 0; }
  .rows { display: grid; gap: var(--space-2); margin: 0; padding: 0; list-style: none; }
  .rows li { display: grid; gap: var(--space-2); min-width: 0; padding: var(--space-3); background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius); }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: var(--space-2); }
  .grid label { display: grid; gap: 3px; min-width: 0; color: var(--text-2); font-size: var(--fs-2); font-weight: 650; }
  .grid input, .grid select { width: 100%; min-width: 0; }
  .remove { justify-self: start; min-height: 30px; padding: 4px 10px; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .done { margin: 0; padding: 8px 10px; font-size: var(--fs-3); border: 1px solid var(--line); border-radius: var(--radius); background: var(--surface-2); }
</style>

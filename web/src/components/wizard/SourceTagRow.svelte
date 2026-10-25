<script lang="ts">
  import { wizard, draftEnvs } from '../../lib/wizard.svelte';
  import { addressOfSource, checkReach, removeChosen, setSourceTag, sourcesView } from '../../lib/wizardSources.svelte';
  import { chipsFor, parseDatabaseRef, sourceKey, suggestionFor, tagProblem } from '../../lib/wizardSources';
  import type { TagField } from '../../lib/wizardSources';
  import type { DraftSource } from '../../lib/wizardFlow';
  import { SOURCES_TEXT, kindLabel, reachedText, tagProblemText, unreachedText } from '../../lib/copy/wizardSourcesCopy';

  // One chosen source with its three names: Environment, then Portal, then API. Each may be left blank. Suggestions are buttons the person
  // presses; nothing is filled in on its own. A name is kept when the field is left or Enter is pressed, so typing is never cut short.
  let { source, onRemoved }: { source: DraftSource; onRemoved?: () => void } = $props();

  const uid = $props.id();
  const key = $derived(sourceKey(source));
  const envNames = $derived(draftEnvs().names);
  const chips = $derived(chipsFor(source, suggestionFor(source, sourcesView.findings), wizard.sources, envNames));
  const address = $derived(addressOfSource(source.ref));
  const reach = $derived(sourcesView.reach[key]);
  const checking = $derived(sourcesView.checking.includes(key));
  const database = $derived(parseDatabaseRef(source.ref)?.database ?? '');
  let problems = $state<Record<TagField, string>>({ env: '', portal: '', api: '' });

  const FIELDS: ReadonlyArray<{ field: TagField; label: string }> = [
    { field: 'env', label: SOURCES_TEXT.envLabel },
    { field: 'portal', label: SOURCES_TEXT.portalLabel },
    { field: 'api', label: SOURCES_TEXT.apiLabel }
  ];

  function commit(field: TagField, input: HTMLInputElement) {
    const found = tagProblem(field, input.value, envNames);
    if (found) { problems[field] = tagProblemText(found); return; }
    problems[field] = '';
    setSourceTag(key, field, input.value);
    // Show the name as it was kept (extra spaces removed).
    const now = wizard.sources.find((s) => sourceKey(s) === key);
    if (now) input.value = now[field];
  }

  function onKey(event: KeyboardEvent, field: TagField) {
    // Enter keeps the name. It must not move the guide on to the next step.
    if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); event.stopPropagation(); commit(field, event.currentTarget as HTMLInputElement); }
  }

  function useChip(field: TagField, text: string) {
    problems[field] = '';
    setSourceTag(key, field, text);
  }
</script>

<li class="row">
  <fieldset class="tags" aria-describedby="{uid}-state">
    <legend class="name">
      <span class="what">{source.label || kindLabel(source.kind)}</span>
      <span class="kind">{kindLabel(source.kind)}{source.classification === 'audit' ? ', ' + SOURCES_TEXT.classAudit : source.classification === 'other' ? ', ' + SOURCES_TEXT.classOther : ''}</span>
    </legend>
    <p class="state" id="{uid}-state">{source.env || source.portal || source.api ? [source.env, source.portal, source.api].filter((p) => p !== '').join(' › ') : SOURCES_TEXT.tagBlank}{database !== '' ? ' (' + database + ')' : ''}</p>

    <div class="fields">
      {#each FIELDS as item (item.field)}
        {@const fieldId = uid + '-' + item.field}
        {@const list = item.field === 'env' ? chips.env : item.field === 'portal' ? chips.portal : chips.api}
        <div class="field">
          <label for={fieldId}>{item.label}</label>
          <input id={fieldId} type="text" autocomplete="off" autocapitalize="off" spellcheck="false" maxlength={item.field === 'env' ? 60 : 80} value={source[item.field]}
            aria-invalid={problems[item.field] ? true : undefined} aria-describedby={problems[item.field] ? fieldId + '-problem' : undefined}
            onchange={(event) => commit(item.field, event.currentTarget)} onkeydown={(event) => onKey(event, item.field)} />
          {#if list.length > 0}
            <ul class="chips" aria-label="{item.field === 'env' ? SOURCES_TEXT.chipsEnv : SOURCES_TEXT.chipsSuggested}: {item.label}">
              {#each list as text (text.toLocaleLowerCase())}
                <li><button type="button" class="chip" aria-label="Use {text} as the {item.label.toLocaleLowerCase()}" onclick={() => useChip(item.field, text)}>{text}</button></li>
              {/each}
            </ul>
          {/if}
          <p class="inline-error" id="{fieldId}-problem" role="alert" hidden={!problems[item.field]}>{problems[item.field]}</p>
        </div>
      {/each}
    </div>

    {#if address !== ''}
      <p class="reach" class:bad={reach?.state === 'failed'} role="status">
        {#if checking}{SOURCES_TEXT.addrChecking}
        {:else if reach?.state === 'ok'}{reachedText(address)}
        {:else if reach?.state === 'failed'}{unreachedText(address, reach.reason)} {SOURCES_TEXT.addrFlagNote}
        {:else}{address}{/if}
      </p>
    {/if}

    <div class="actions">
      {#if address !== '' && reach?.state === 'failed'}
        <button type="button" class="secondary-button" disabled={checking} onclick={() => void checkReach(key)}>{SOURCES_TEXT.addrCheckAgain}</button>
      {/if}
      <button type="button" class="quiet-button" aria-label="{SOURCES_TEXT.removeSource} {source.label || kindLabel(source.kind)}" onclick={() => { removeChosen(key); onRemoved?.(); }}>{SOURCES_TEXT.removeSource}</button>
    </div>
  </fieldset>
</li>

<style>
  .row { list-style: none; min-width: 0; }
  .tags { display: grid; gap: var(--space-2); min-width: 0; margin: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .name { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: baseline; padding: 0 var(--space-2); max-width: 100%; }
  .what { font-size: var(--fs-4); font-weight: 600; overflow-wrap: anywhere; }
  .kind { color: var(--text-2); font-size: var(--fs-2); }
  .state { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .fields { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: var(--space-3); min-width: 0; }
  .field { display: grid; gap: var(--space-1); align-content: start; min-width: 0; }
  .field label { font-size: var(--fs-3); font-weight: 600; }
  .field input { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 44px; padding: 8px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-4); }
  .field input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .field input[aria-invalid='true'] { border-color: var(--bad); }
  .chips { display: flex; flex-wrap: wrap; gap: var(--space-1); margin: 0; padding: 0; list-style: none; }
  .chip { min-height: 32px; max-width: 100%; padding: 2px var(--space-3); color: var(--text); background: var(--surface-2); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-3); overflow-wrap: anywhere; cursor: pointer; }
  .chip:hover { border-color: var(--accent); }
  .chip:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .inline-error { margin: 0; }
  .inline-error[hidden] { display: none; }
  .reach { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .reach.bad { color: var(--text); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>

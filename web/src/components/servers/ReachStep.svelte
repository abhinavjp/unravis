<script lang="ts">
  import StepFooter from './StepFooter.svelte';
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { FOLDER_KINDS, accessCommands, folderProblem, testChecks, testVerdict, failureText } from '../../lib/servers';
  import CommandBlock from './CommandBlock.svelte';

  // Step 4. First the folders Unravis may read on this server, then a check per machine. A machine's check lists only what the
  // server answered: the machine, each folder and how many files it found.
  let { flow }: { flow: ServersFlow } = $props();

  let drafts = $state<Record<string, string>>({});
  let draftNote = $state<Record<string, string>>({});

  const server = $derived(flow.server);
  const machines = $derived(server?.minionIds ?? []);
  const folderCount = $derived(Object.values(server?.folders ?? {}).reduce((n, list) => n + list.length, 0));
  const anyReady = $derived(flow.readable.length > 0);
  const commandInput = $derived(server ? { os: server.os, host: server.host, port: server.port, user: server.user, machines, folders: server.folders, sudo: !!server.saltSudo } : null);

  async function add(kind: string, path?: string) {
    const value = (path ?? drafts[kind] ?? '').trim();
    const problem = folderProblem(value);
    draftNote[kind] = problem;
    if (problem) return;
    if (await flow.addFolder(kind, value)) { drafts[kind] = ''; draftNote[kind] = ''; }
  }
</script>

<section class="sv-block" aria-labelledby="sv-reach-title">
  <h3 id="sv-reach-title">{T.reachTitle}</h3>
  <p class="sv-lead">{T.reachLead}</p>

  <details class="sv-folders">
    <summary><strong>{T.foldersHeading}</strong> <span class="sv-note">{T.folderSummary(folderCount)}</span></summary>
    {#each FOLDER_KINDS as info (info.kind)}
      {@const list = server?.folders?.[info.kind] ?? []}
      <div class="sv-folder-kind">
        <div class="sv-folder-head"><strong>{info.label}</strong> <span class="sv-note">{info.hint}</span></div>
        {#if list.length === 0}<p class="sv-note">{T.foldersEmpty}</p>{/if}
        <ul class="sv-chips">
          {#each list as path (path)}
            <li class="sv-chip sv-path"><code>{path}</code>
              <button type="button" class="quiet-button" disabled={!!flow.busy} aria-label={T.foldersRemove + ' ' + path} onclick={() => flow.removeFolder(info.kind, path)}>{T.foldersRemove}</button></li>
          {/each}
        </ul>
        <div class="sv-add">
          <input type="text" aria-label={info.label + ': ' + T.foldersPath} placeholder={T.foldersPath} spellcheck="false" autocomplete="off" bind:value={drafts[info.kind]} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); add(info.kind); } }} />
          <button type="button" class="secondary-button" disabled={!!flow.busy || !(drafts[info.kind] ?? '').trim()} onclick={() => add(info.kind)}>{T.foldersAdd}</button>
        </div>
        {#if draftNote[info.kind]}<p class="sv-bad" role="alert">{draftNote[info.kind]}</p>{/if}
        {#if info.suggestions.some((s) => !list.includes(s))}
          <p class="sv-suggest"><span class="sv-note">{T.foldersSuggest}</span>
            {#each info.suggestions.filter((s) => !list.includes(s)) as s (s)}<button type="button" class="quiet-button" disabled={!!flow.busy} onclick={() => add(info.kind, s)}><code>{s}</code></button>{/each}</p>
        {/if}
      </div>
    {/each}
    {#if flow.busy === 'folders'}<p class="sv-note" role="status">{T.foldersSaving}</p>{/if}
  </details>

  <div class="sv-machines">
    {#each machines as m (m)}
      {@const t = flow.tests[m]}
      <div class="sv-panel">
        <header><h4>{m}</h4>{#if t?.state === 'done' && t.result?.host.description}<span class="sv-sub">{t.result.host.description}</span>{/if}<span class="sv-sp"></span>
          {#if t?.state === 'done'}
            {@const v = testVerdict(t.result ?? null)}
            <span class="sv-chip sv-tone-{v === 'ready' ? 'ok' : v === 'partial' ? 'pending' : 'bad'}">{v === 'ready' ? T.reachOkLine : v === 'partial' ? T.reachPartLine : T.reachNoneLine}</span>
          {:else if t?.state === 'running'}<span class="sv-sub" role="status">{T.reachChecking}</span>{:else if t?.state === 'failed'}<span class="sv-chip sv-tone-bad">{T.reachNoAnswer}</span>{/if}
          <button type="button" class="secondary-button sv-small" disabled={folderCount === 0 || t?.state === 'running' || !!flow.busy} onclick={() => flow.testMachine(m)}>{t ? T.reachRecheck : T.reachCheck}</button>
        </header>
        {#if t?.state === 'failed' && t.failure}
          <p class="sv-in sv-bad" role="alert">{failureText(t.failure)}</p>
        {:else if t?.state === 'done' && t.result}
          <ul class="sv-checks">
            {#each testChecks(t.result, folderCount) as line (line.id)}
              <li class:sv-line-bad={!line.ok}><span class="sv-mark" aria-hidden="true">{line.ok ? '✓' : '✕'}</span><span class="sv-sr">{line.ok ? 'Passed. ' : 'Not passed. '}</span><span>{line.text}{#if line.note}<span class="sv-sub"> {line.note}</span>{/if}</span></li>
            {/each}
          </ul>
        {:else if !t}
          <p class="sv-in sv-sub">{T.reachNotChecked}</p>
        {/if}
      </div>
    {/each}
    {#if folderCount === 0}<p class="sv-note">{T.reachNoFolders}</p>{/if}
  </div>

  {#if commandInput}
    <div class="sv-help">
      <CommandBlock label={T.reachCommands} text={accessCommands('salt', commandInput)} />
      <CommandBlock label={T.reachAdmin} text={accessCommands('admin-salt', commandInput)} />
    </div>
  {/if}

  <StepFooter back={() => flow.go("pick")} primary={{ label: T.reachGo, onclick: () => flow.openHas(), disabled: !anyReady || !!flow.busy }} reason={T.reachNeed} />
</section>

<style>
  .sv-folders { display: grid; gap: var(--space-3); }
  .sv-folder-kind { display: grid; gap: var(--space-2); padding-top: var(--space-2); border-top: 1px solid var(--line); min-width: 0; }
  .sv-folder-head { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: baseline; }
  .sv-chips { display: flex; flex-wrap: wrap; gap: var(--space-2); margin: 0; padding: 0; list-style: none; }
  .sv-path { display: inline-flex; align-items: center; gap: var(--space-2); max-width: 100%; }
  .sv-path code { font-family: var(--mono); font-size: 12px; overflow-wrap: anywhere; }
  .sv-add { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .sv-add input { flex: 1 1 240px; min-width: 0; padding: 6px 8px; font: inherit; font-family: var(--mono); font-size: 12px; color: var(--text); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); box-sizing: border-box; }
  .sv-suggest { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1); margin: 0; }
  .sv-suggest code { font-family: var(--mono); font-size: 12px; }
  .sv-machines { display: grid; gap: var(--space-3); }
  .sv-small { padding: 4px 10px; }
  .sv-checks { margin: 0; padding: 0 14px; list-style: none; font-size: var(--fs-3); }
  .sv-checks li { display: flex; gap: 10px; padding: 9px 0; border-bottom: 1px solid var(--line); overflow-wrap: anywhere; }
  .sv-checks li:last-child { border-bottom: 0; }
  .sv-checks .sv-sub { margin-left: 8px; }
  .sv-mark { width: 1.2ch; color: var(--tone-ok); font-weight: 700; }
  .sv-line-bad .sv-mark { color: var(--bad); }
  .sv-sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  .sv-help { display: grid; gap: var(--space-2); }
</style>



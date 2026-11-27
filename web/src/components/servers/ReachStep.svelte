<script lang="ts">
  import { untrack } from 'svelte';
  import StepFooter from './StepFooter.svelte';
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { FOLDER_KINDS, missingQuickFolders, accessCommands, folderProblem, folderLine, testChecks, testVerdict, suggestionsFrom, failureText, formatBytes, type CheckLevel } from '../../lib/servers';
  import CommandBlock from './CommandBlock.svelte';

  // Step 4. The folders Unravis may read on this server, each with what the last check found, then a check per machine. A machine's
  // check lists only what the server answered. The recorder on the machine can say where its folders are, and each can be added in one click.
  let { flow }: { flow: ServersFlow } = $props();

  let drafts = $state<Record<string, string>>({});
  let draftNote = $state<Record<string, string>>({});
  let asked = $state<Record<string, boolean>>({});

  const server = $derived(flow.server);
  const machines = $derived(server?.minionIds ?? []);
  const folderCount = $derived(Object.values(server?.folders ?? {}).reduce((n, list) => n + list.length, 0));
  let addOpen = $state(untrack(() => folderCount === 0));
  const anyReady = $derived(flow.readable.length > 0);
  const commandInput = $derived(server ? { os: server.os, host: server.host, port: server.port, user: server.user, machines, folders: server.folders, sudo: !!server.saltSudo } : null);

  const MARK: Record<CheckLevel, string> = { ok: '✓', warn: '!', bad: '✕' };
  const WORD: Record<CheckLevel, string> = { ok: 'Passed. ', warn: T.reachWarnMark, bad: 'Not passed. ' };
  const RANK: Record<CheckLevel, number> = { ok: 0, warn: 1, bad: 2 };

  /** What the checks found in one allowed folder, worst case across the machines checked. `null` until a check has run. */
  function folderState(kind: string, path: string): { level: CheckLevel; files: number; note: string } | null {
    let worst: { level: CheckLevel; files: number; note: string } | null = null;
    let files = 0;
    for (const m of machines) {
      const t = flow.tests[m];
      const f = t?.state === 'done' ? t.result?.folders.find((x) => x.kind === kind && x.path === path) : undefined;
      if (!f) continue;
      const line = folderLine(f);
      files += f.ok ? f.files : 0;
      if (!worst || RANK[line.level] > RANK[worst.level]) worst = { level: line.level, files: 0, note: line.note ?? '' };
    }
    if (worst) worst.files = files;
    return worst;
  }

  async function add(kind: string, path?: string) {
    const value = (path ?? drafts[kind] ?? '').trim();
    const problem = folderProblem(value);
    draftNote[kind] = problem;
    if (problem) return;
    if (await flow.addFolder(kind, value)) { drafts[kind] = ''; draftNote[kind] = ''; }
  }
  function find(machine: string) { asked[machine] = true; void flow.discover(machine); }
</script>

<section class="sv-block" aria-labelledby="sv-reach-title">
  <h3 id="sv-reach-title">{T.reachTitle}</h3>
  <p class="sv-lead">{T.reachLead}</p>

  {#if folderCount === 0}
    <div class="notice">
      <p>{T.foldersWhy}</p>
      <button type="button" class="primary-button" disabled={!!flow.busy} onclick={() => flow.addFolders(missingQuickFolders(server?.folders))}>{T.foldersAddSuggested}</button>
      {#if flow.busy === 'folders'}<p class="sv-note" role="status">{T.foldersSaving}</p>{/if}
    </div>
  {:else}
    <div class="sv-folderlist">
      <h4>{T.reachFolderList}</h4>
      {#each FOLDER_KINDS as info (info.kind)}
        {@const list = server?.folders?.[info.kind] ?? []}
        {#if list.length > 0}
          <div class="sv-folder-kind">
            <div class="sv-folder-head"><strong>{info.label}</strong> <span class="sv-note">{info.hint}</span></div>
            <ul class="sv-paths">
              {#each list as path (path)}
                {@const st = folderState(info.kind, path)}
                <li>
                  {#if st}<span class="sv-mark sv-lvl-{st.level}" aria-hidden="true">{MARK[st.level]}</span><span class="sv-sr">{WORD[st.level]}</span>
                  {:else}<span class="sv-mark sv-lvl-none" aria-hidden="true">–</span><span class="sv-sr">{T.reachFolderUnchecked}. </span>{/if}
                  <code>{path}</code>
                  <span class="sv-note">{st ? (st.level === 'ok' ? T.reachFolderFiles(st.files) : st.note) : T.reachFolderUnchecked}</span>
                  <button type="button" class="quiet-button" disabled={!!flow.busy} aria-label={T.foldersRemove + ' ' + path} onclick={() => flow.removeFolder(info.kind, path)}>{T.foldersRemove}</button>
                </li>
              {/each}
            </ul>
          </div>
        {/if}
      {/each}
    </div>
  {/if}

  <details class="sv-folders" bind:open={addOpen}>
    <summary><strong>{T.reachAddFolder}</strong> <span class="sv-note">{T.folderSummary(folderCount)}</span></summary>
    <p class="sv-note">{T.foldersWhyShort}</p>
    {#each FOLDER_KINDS as info (info.kind)}
      {@const list = server?.folders?.[info.kind] ?? []}
      <div class="sv-folder-kind">
        <div class="sv-folder-head"><strong>{info.label}</strong> <span class="sv-note">{info.hint}</span></div>
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
      {@const verdict = t?.state === 'done' ? testVerdict(t.result ?? null) : null}
      {@const d = flow.discoveries[m]}
      {@const found = suggestionsFrom(d?.discovery, server?.folders)}
      <div class="sv-panel">
        <header><h4>{m}</h4>{#if t?.state === 'done' && t.result?.host.description}<span class="sv-sub">{t.result.host.description}</span>{/if}<span class="sv-sp"></span>
          {#if verdict}
            <span class="sv-chip sv-tone-{verdict === 'ready' ? 'ok' : verdict === 'none' ? 'bad' : 'pending'}">{verdict === 'ready' ? T.reachOkLine : verdict === 'partial' ? T.reachPartLine : verdict === 'empty' ? T.reachEmptyLine : T.reachNoneLine}</span>
          {:else if t?.state === 'running'}<span class="sv-sub" role="status">{T.reachChecking}</span>{:else if t?.state === 'failed'}<span class="sv-chip sv-tone-bad">{T.reachNoAnswer}</span>{/if}
          <button type="button" class="secondary-button sv-small" disabled={d?.state === 'running' || t?.state === 'running' || !!flow.busy} onclick={() => find(m)}>{T.reachFind}</button>
          <button type="button" class="secondary-button sv-small" disabled={folderCount === 0 || t?.state === 'running' || !!flow.busy} onclick={() => flow.testMachine(m)}>{t ? T.reachRecheck : T.reachCheck}</button>
        </header>
        {#if t?.state === 'failed' && t.failure}
          <p class="sv-in sv-bad" role="alert">{failureText(t.failure)}</p>
        {:else if t?.state === 'done' && t.result}
          <ul class="sv-checks">
            {#each testChecks(t.result, folderCount) as line (line.id)}
              <li class="sv-lvl-row-{line.level}"><span class="sv-mark sv-lvl-{line.level}" aria-hidden="true">{MARK[line.level]}</span><span class="sv-sr">{WORD[line.level]}</span><span>{line.text}{#if line.note}<span class="sv-sub"> {line.note}</span>{/if}</span></li>
            {/each}
          </ul>
        {:else if !t}
          <p class="sv-in sv-sub">{T.reachNotChecked}</p>
        {/if}

        {#if d && (found.length > 0 || asked[m] || (verdict && verdict !== 'ready'))}
          <div class="sv-found" aria-live="polite">
            {#if d.state === 'running'}<p class="sv-sub" role="status">{T.reachFinding}</p>
            {:else if d.state === 'failed'}<p class="sv-bad" role="alert">{T.reachFoundFailed}{#if d.failure} {failureText(d.failure)}{/if}</p>
            {:else if found.length > 0}
              <h5>{T.reachFoundHeading}</h5>
              <ul class="sv-checks">
                {#each found as s (s.kind + '|' + s.path)}
                  <li><span class="sv-mark sv-lvl-ok" aria-hidden="true">+</span>
                    <span><strong>{s.label}</strong> <code>{s.path}</code><span class="sv-sub"> {T.reachFolderFiles(s.files)}, {formatBytes(s.bytes)}</span></span>
                    <button type="button" class="secondary-button sv-small" disabled={!!flow.busy} onclick={() => flow.addSuggestions(m, [s])}>{T.reachAddOne}</button></li>
                {/each}
              </ul>
              {#if found.length > 1}<div class="sv-actions"><button type="button" class="primary-button sv-small" disabled={!!flow.busy} onclick={() => flow.addSuggestions(m, found)}>{T.reachAddAll(found.length)}</button></div>{/if}
            {:else if d.discovery && !d.discovery.found}<p class="sv-note">{d.discovery.note || T.reachFoundNotPublished}</p>
            {:else}<p class="sv-note">{T.reachFoundNone}</p>{/if}
          </div>
        {/if}
      </div>
    {/each}
    {#if folderCount === 0}<p class="sv-note">{T.reachNoFolders}</p>{/if}
  </div>

  {#if commandInput}
    <details class="sv-help"><summary>{T.pickHelp}</summary>
      <CommandBlock label={T.reachCommands} text={accessCommands('salt', commandInput)} />
      <CommandBlock label={T.reachAdmin} text={accessCommands('admin-salt', commandInput)} />
    </details>
  {/if}

  <StepFooter back={() => flow.go("pick")} primary={{ label: T.reachGo, onclick: () => flow.openHas(), disabled: !anyReady || !!flow.busy }} reason={T.reachNeed} />
</section>

<style>
  .sv-folders { display: grid; gap: var(--space-3); }
  .sv-folderlist { display: grid; gap: var(--space-2); }
  .sv-folderlist h4, .sv-found h5 { margin: 0; }
  .sv-folder-kind { display: grid; gap: var(--space-2); padding-top: var(--space-2); border-top: 1px solid var(--line); min-width: 0; }
  .sv-folder-head { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: baseline; }
  .sv-paths { margin: 0; padding: 0; list-style: none; display: grid; gap: var(--space-2); }
  .sv-paths li { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-2); min-width: 0; }
  .sv-paths code { font-family: var(--mono); font-size: 12px; overflow-wrap: anywhere; }
  .sv-add { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .sv-add input { flex: 1 1 240px; min-width: 0; padding: 6px 8px; font: inherit; font-family: var(--mono); font-size: 12px; color: var(--text); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); box-sizing: border-box; }
  .sv-suggest { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1); margin: 0; }
  .sv-suggest code { font-family: var(--mono); font-size: 12px; }
  .sv-machines { display: grid; gap: var(--space-3); }
  .sv-small { padding: 4px 10px; }
  .sv-checks { margin: 0; padding: 0 14px; list-style: none; font-size: var(--fs-3); }
  .sv-checks li { display: flex; gap: 10px; padding: 9px 0; border-bottom: 1px solid var(--line); overflow-wrap: anywhere; align-items: baseline; }
  .sv-checks li:last-child { border-bottom: 0; }
  .sv-checks .sv-sub { margin-left: 8px; }
  .sv-checks li button { margin-left: auto; flex: none; }
  .sv-found { display: grid; gap: var(--space-2); padding: 0 14px 12px; }
  .sv-mark { display: inline-block; min-width: 1.2ch; font-weight: 700; }
  .sv-lvl-ok { color: var(--tone-ok); }
  .sv-lvl-warn { color: var(--warn); }
  .sv-lvl-bad { color: var(--bad); }
  .sv-lvl-none { color: var(--text-2); }
  .sv-sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  .sv-help { display: grid; gap: var(--space-2); }
</style>

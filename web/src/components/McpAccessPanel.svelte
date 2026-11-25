<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from '../lib/api';
  import { MCP_TEXT as T, toolLabel } from '../lib/copy/mcpGrantsCopy';
  import { approveBody, chipOf, defaultDraft, groupProfiles, inFuture, ago, scopeLine, sourceNames, type McpDraft, type McpList, type McpProfile } from '../lib/mcpGrants';
  import { McpApiError, listHosts, mcpApi } from '../lib/mcpGrantsApi';
  import { visiblePoll } from '../lib/visibility';
  import Drawer from './Drawer.svelte';
  import McpApprovalCard from './McpApprovalCard.svelte';

  // AI access (BL-347): the person's side of MCP. The list of hosts that asked, the approval card of each waiting host, the approved
  // hosts with revoke and renew, the ended ones, and the one global Stop. The host's own words are plain text everywhere; there is no
  // raw-HTML tag in this screen. The list is read again every few seconds while the panel is open, so a program that connects appears at once.
  let { onClose }: { onClose: () => void } = $props();

  const POLL_MS = 5000;
  let list = $state<McpList | null>(null);
  let sources = $state<{ id: string; label: string }[]>([]);
  let loading = $state(true);
  let loadError = $state('');
  let refreshing = $state(false);
  // One form per waiting host and revision: a host that is reissued and connects again starts a fresh form.
  let drafts = $state<Record<string, McpDraft>>({});
  let busy = $state<Record<string, string>>({});
  let problems = $state<Record<string, string>>({});
  let note = $state('');
  let stopBusy = $state('');
  let stopError = $state('');
  let stopWarn = $state('');
  let element = $state<HTMLElement>();

  const groups = $derived(groupProfiles(list?.profiles ?? []));
  const labels = $derived(new Map(sources.map((s) => [s.id, s.label] as const)));
  const stopped = $derived(list?.stopped === true);
  const now = $derived(list?.now ?? '');
  const draftKey = (p: McpProfile) => p.id + ':' + p.rev;
  const messageOf = (error: unknown) => (error instanceof Error ? error.message : T.actionFailed);

  // A form exists for every waiting host once the list and the sources are read (never made while the page is drawn).
  function ensureDrafts(fresh: McpList) {
    const live = new Set<string>();
    for (const p of fresh.profiles) {
      if (p.state !== 'pending') continue;
      const k = draftKey(p);
      live.add(k);
      if (!drafts[k]) drafts[k] = defaultDraft(fresh.tiers, fresh.limits, sources.map((s) => s.id));
    }
    for (const k of Object.keys(drafts)) if (!live.has(k)) delete drafts[k];
  }

  let reloadAgain = false;
  async function load(initial = false) {
    if (refreshing) { reloadAgain = true; return; } // a read that began before an action must not be the last word
    refreshing = true;
    if (initial) loading = true;
    try {
      const [fresh, connected] = await Promise.all([listHosts(), api.sources().catch(() => null)]);
      if (connected) sources = connected.map((s) => ({ id: s.id, label: s.label || s.id }));
      ensureDrafts(fresh);
      list = fresh;
      loadError = '';
    } catch (error) {
      loadError = messageOf(error);
    } finally {
      refreshing = false;
      loading = false;
    }
    if (reloadAgain) { reloadAgain = false; await load(); }
  }

  onMount(() => {
    void load(true);
    // Read again while open, but not in a hidden tab (visiblePoll) and not while a decision is being sent.
    return visiblePoll(async () => { if (Object.keys(busy).length === 0 && !stopBusy) await load(); }, POLL_MS);
  });

  // The card that was acted on leaves the page, so the keyboard goes to the panel heading rather than to nothing.
  async function settleFocus() {
    await tick();
    const active = document.activeElement;
    if (!active || active === document.body || !element?.contains(active)) element?.querySelector<HTMLElement>('h2')?.focus();
  }

  async function act(id: string, label: string, run: () => Promise<unknown>, done: string) {
    if (busy[id]) return;
    busy[id] = label;
    delete problems[id];
    note = '';
    try {
      await run();
      note = done;
    } catch (error) {
      if (error instanceof McpApiError && error.code === 'stale') note = T.staleNote;
      else problems[id] = messageOf(error);
    } finally {
      delete busy[id];
    }
    await load();
    await settleFocus();
  }

  const approve = (p: McpProfile) => {
    // approveBody throws without the card's revision; it is always p.rev here.
    return act(p.id, 'approve', () => mcpApi.approve(p.id, approveBody(p, drafts[draftKey(p)], list?.tiers ?? [])), T.doneApproved);
  };
  const deny = (p: McpProfile) => act(p.id, 'deny', () => mcpApi.deny(p.id, p.rev), T.doneDenied);
  const revoke = (p: McpProfile) => act(p.id, 'revoke', () => mcpApi.revoke(p.id, p.rev), T.doneRevoked);
  const renew = (p: McpProfile) => act(p.id, 'renew', () => mcpApi.renew(p.id, p.rev), T.doneRenewed);
  const reissue = (p: McpProfile) => act(p.id, 'reissue', () => mcpApi.reissue(p.id, p.rev), T.doneReissued);
  const remove = (p: McpProfile) => act(p.id, 'remove', () => mcpApi.remove(p.id, p.rev), T.doneRemoved);

  async function stop() {
    if (stopBusy) return;
    stopBusy = 'stop'; stopError = ''; stopWarn = '';
    try {
      const result = await mcpApi.stop();
      if (!result.saved) stopWarn = T.stopNotSaved;
    } catch { stopError = T.stopFailed; }
    finally { stopBusy = ''; }
    await load();
  }
  async function resume() {
    if (stopBusy) return;
    stopBusy = 'resume'; stopError = ''; stopWarn = '';
    try { await mcpApi.resume(); } catch { stopError = T.resumeFailed; }
    finally { stopBusy = ''; }
    await load();
  }
</script>

<Drawer title={T.title} titleId="mcp-title" intro={T.intro} closeLabel={T.closeLabel} class="mcp-drawer" focusTitle bind:element {onClose}>
  <div class="mcp-body" data-testid="mcp-panel">
    {#if loading}
      <p class="field-note" role="status">{T.loading}</p>
    {:else if loadError && !list}
      <div class="inline-error" role="alert">{loadError}</div>
      <div class="actions"><button type="button" class="secondary-button" onclick={() => load(true)}>{T.retry}</button></div>
    {:else if list && list.mode === 'legacy'}
      <section class="state-note" data-testid="mcp-legacy"><h3>{T.legacyTitle}</h3><p>{T.legacyBody}</p></section>
    {:else if list && list.mode === 'off'}
      <section class="state-note" data-testid="mcp-off"><h3>{T.offTitle}</h3><p>{list.reason === 'sealing' ? T.offSealing : T.offStore}</p></section>
    {:else if list}
      <!-- The global stop: one control, always at the top, never scrolled away. -->
      <div class="stopbar" class:on={stopped} data-testid="mcp-stopbar">
        {#if stopped}
          <div class="stop-text"><strong>{T.stoppedTitle}</strong><span>{T.stoppedBody}</span></div>
          <button type="button" class="primary-button" data-testid="mcp-resume" disabled={!!stopBusy} onclick={resume}>{stopBusy === 'resume' ? T.resuming : T.resumeButton}</button>
        {:else}
          <button type="button" class="primary-button danger-fill" data-testid="mcp-stop" title={T.stopTitle} aria-describedby="mcp-stop-help" disabled={!!stopBusy} onclick={stop}>{stopBusy === 'stop' ? T.stopping : T.stopButton}</button>
          <span id="mcp-stop-help" class="field-note">{T.stopTitle}</span>
        {/if}
      </div>
      {#if stopError}<div class="inline-error" role="alert">{stopError}</div>{/if}
      {#if stopWarn}<div class="inline-error" role="alert" data-testid="mcp-stop-warn">{stopWarn}</div>{/if}
      {#if loadError}<div class="inline-error" role="alert">{loadError} <button type="button" class="quiet-button" onclick={() => load()}>{T.retry}</button></div>{/if}
      <p class="notice" role="status" hidden={!note} data-testid="mcp-note">{note}</p>

      <section aria-labelledby="mcp-wait-h" class="group">
        <h3 id="mcp-wait-h">{T.waitingTitle} <span class="count">{groups.pending.length}</span></h3>
        {#if groups.pending.length === 0}<p class="field-note">{T.waitingNone}</p>{/if}
        {#each groups.pending as p (p.id)}
          {#if drafts[draftKey(p)]}<McpApprovalCard profile={p} draft={drafts[draftKey(p)]} onChange={(next) => { drafts[draftKey(p)] = next; }} tiers={list.tiers} limits={list.limits} {sources} {now} busy={busy[p.id] ?? ''} error={problems[p.id] ?? ''}
            onApprove={() => approve(p)} onDeny={() => deny(p)} onStartOver={() => reissue(p)} onRemove={() => remove(p)} />{/if}
        {/each}
      </section>

      <section aria-labelledby="mcp-ok-h" class="group">
        <h3 id="mcp-ok-h">{T.approvedTitle} <span class="count">{groups.active.length}</span></h3>
        {#if groups.active.length === 0}<p class="field-note">{T.approvedNone}</p>{/if}
        {#each groups.active as p (p.id)}
          {@const chip = chipOf(p, stopped)}
          <article class="row" data-testid="mcp-row" data-profile={p.id}>
            <div class="row-head">
              <h4><span data-testid="mcp-row-name">{p.hostName || p.id}</span></h4>
              <span class="chip-state {chip.tone}" data-testid="mcp-row-state">{chip.label}</span>
            </div>
            <p class="scope"><span class="k">{T.scopeLabel}</span> {scopeLine(p)}</p>
            <p class="meta">
              <span><span class="k">{T.lastCallLabel}</span> {p.lastCallAt ? ago(now, p.lastCallAt) : T.neverCalled}</span>
              {#if p.expiresAt}<span><span class="k">{T.expiresLabel}</span> {inFuture(now, p.expiresAt)}</span>{/if}
              {#if p.identifiers && p.identifiersUntil}<span><span class="k">{T.identifiersUntilLabel}</span> {inFuture(now, p.identifiersUntil)}</span>{/if}
            </p>
            {#if p.code}<p class="scope" data-testid="mcp-row-code"><span class="k">{T.codeLegend}</span> {T.codeOnLine}</p>{/if}
            {#if p.expired}<p class="field-note">{T.expiredNote}</p>{:else if !p.claimed}<p class="field-note">{T.notCollectedNote}</p>{/if}
            <details>
              <summary>{T.details}</summary>
              <dl>
                <dt>{T.pathLabel}</dt><dd class="path">{p.exe || '-'}</dd>
                <dt>{T.versionLabel}</dt><dd>{p.hostVersion || '-'}</dd>
                <dt>{T.toolsLabel}</dt><dd>{p.tools.map(toolLabel).join(', ')}</dd>
                <dt>{T.sourcesLabel}</dt><dd>{sourceNames(p.sources, labels).join(', ')}</dd>
              </dl>
              <p class="field-note">{T.reportedLabel}</p>
            </details>
            {#if problems[p.id]}<div class="inline-error" role="alert">{problems[p.id]}</div>{/if}
            <div class="actions">
              <button type="button" class="secondary-button" data-testid="mcp-revoke" title={T.revokeHelp} disabled={!!busy[p.id]} onclick={() => revoke(p)}>{busy[p.id] === 'revoke' ? T.revoking : T.revoke}</button>
              <button type="button" class="secondary-button" data-testid="mcp-renew" disabled={!!busy[p.id]} onclick={() => renew(p)}>{busy[p.id] === 'renew' ? T.renewing : T.renew}</button>
              <button type="button" class="quiet-button" data-testid="mcp-reissue" disabled={!!busy[p.id]} onclick={() => reissue(p)}>{busy[p.id] === 'reissue' ? T.reissuing : T.reissue}</button>
            </div>
          </article>
        {/each}
      </section>

      {#if groups.ended.length > 0}
        <section aria-labelledby="mcp-end-h" class="group">
          <h3 id="mcp-end-h">{T.endedTitle} <span class="count">{groups.ended.length}</span></h3>
          {#each groups.ended as p (p.id)}
            {@const chip = chipOf(p, stopped)}
            <article class="row ended" data-testid="mcp-row" data-profile={p.id}>
              <div class="row-head">
                <h4><span data-testid="mcp-row-name">{p.hostName || p.id}</span></h4>
                <span class="chip-state {chip.tone}" data-testid="mcp-row-state">{chip.label}</span>
              </div>
              <p class="field-note">{p.state === 'revoked' ? T.endedRevoked : T.endedDenied}</p>
              {#if problems[p.id]}<div class="inline-error" role="alert">{problems[p.id]}</div>{/if}
              <div class="actions">
                <button type="button" class="secondary-button" data-testid="mcp-again" title={T.allowAgainHelp} disabled={!!busy[p.id]} onclick={() => reissue(p)}>{busy[p.id] === 'reissue' ? T.reissuing : T.allowAgain}</button>
                <button type="button" class="quiet-button" data-testid="mcp-remove-ended" disabled={!!busy[p.id]} onclick={() => remove(p)}>{busy[p.id] === 'remove' ? T.removing : T.removeEntry}</button>
              </div>
            </article>
          {/each}
        </section>
      {/if}
    {/if}
  </div>
</Drawer>

<style>
  :global(.connections-drawer.mcp-drawer) { width: min(760px, calc(100vw - 24px)); }
  @media (max-width: 720px) { :global(.connections-drawer.mcp-drawer) { width: min(760px, 100vw); } }
  .mcp-body { display: grid; gap: var(--space-4); align-content: start; min-width: 0; margin-top: var(--space-3); }
  .group { display: grid; gap: var(--space-3); min-width: 0; }
  h3 { margin: 0; font-size: 15px; font-weight: 600; }
  h4 { margin: 0; font-size: 14px; font-weight: 600; overflow-wrap: anywhere; }
  .count { margin-left: 6px; padding: 1px 8px; color: var(--text-2); background: var(--surface-2); border: 1px solid var(--line); border-radius: 999px; font-size: 12px; font-weight: 500; }
  .state-note { display: grid; gap: var(--space-2); padding: var(--space-3) var(--space-4); background: var(--surface-2); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .state-note p { margin: 0; font-size: 13px; }
  .stopbar { position: sticky; top: 0; z-index: 2; display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); padding: var(--space-2) 0; background: var(--surface); }
  .stopbar.on { padding: var(--space-3); background: color-mix(in srgb, var(--bad) 12%, var(--surface)); border: 1px solid var(--bad); border-radius: var(--radius-sm); }
  .stop-text { display: grid; gap: 2px; flex: 1 1 260px; font-size: 13px; }
  .danger-fill:not(:disabled) { background: var(--bad); border-color: var(--bad); color: var(--bg); }
  .notice[hidden] { display: none; }
  .row { display: grid; gap: var(--space-2); min-width: 0; padding: var(--space-3) var(--space-4); background: var(--surface-2); border: 1px solid var(--line); border-left: 3px solid var(--tone-ok); border-radius: var(--radius-sm); }
  .row.ended { border-left-color: var(--pending); }
  .row-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-2); }
  .chip-state { padding: 2px 10px; font-size: 12px; font-weight: 600; color: var(--text); border: 1px solid var(--control-line); border-radius: 999px; }
  .chip-state.ok { border-color: var(--tone-ok); }
  .chip-state.wait { border-color: var(--warn); }
  .chip-state.bad { border-color: var(--bad); }
  .scope, .meta { margin: 0; font-size: 13px; overflow-wrap: anywhere; }
  .meta { display: flex; flex-wrap: wrap; gap: var(--space-1) var(--space-4); color: var(--text-2); }
  .k { color: var(--text-2); font-weight: 600; margin-right: 4px; }
  details summary { width: fit-content; font-size: 13px; font-weight: 500; }
  dl { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: 3px var(--space-3); margin: var(--space-2) 0 0; font-size: 13px; }
  dt { color: var(--text-2); }
  dd { margin: 0; overflow-wrap: anywhere; }
  .path { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; }
  .field-note { margin: 0; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>

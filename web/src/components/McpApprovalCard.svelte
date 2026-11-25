<script lang="ts">
  import { MCP_TEXT as T, toolLabel } from '../lib/copy/mcpGrantsCopy';
  import { IDENTIFIER_CHOICES, IDLE_CHOICES, ago, draftProblem, span, toggled, toolsOf, withTier, type McpDraft, type McpLimits, type McpProfile, type McpTier, type McpTierInfo } from '../lib/mcpGrants';

  // The approval card of one host that waits for the person's decision (BL-347). Everything the host said about itself is shown as plain
  // text under "Reported by the program itself": a name is only a name, a path only a path. Nothing is allowed until Approve is pressed,
  // and Approve sends the revision this card shows (the panel builds the body with approveBody).
  let { profile, draft, onChange, tiers, limits, sources, now, busy, error, onApprove, onDeny, onStartOver, onRemove }: {
    profile: McpProfile;
    draft: McpDraft;
    onChange: (next: McpDraft) => void;
    tiers: McpTierInfo[];
    limits: McpLimits;
    sources: { id: string; label: string }[];
    now: string;
    busy: string;
    error: string;
    onApprove: () => void;
    onDeny: () => void;
    onStartOver: () => void;
    onRemove: () => void;
  } = $props();

  const key = $derived('mcp-' + profile.id);
  const problem = $derived(draftProblem(profile, draft, { notConnected: T.needConnected, noTools: T.needTools, noSources: T.needSources, contested: T.needAccept, notWaiting: T.notWaiting }));
  const noSources = $derived(sources.length === 0);
  const idleChoices = $derived(IDLE_CHOICES.filter((m) => m >= limits.minIdleMinutes && m <= limits.maxIdleMinutes));
  const identChoices = $derived(IDENTIFIER_CHOICES.filter((m) => m <= limits.maxIdentifiersMinutes));
  const seen = $derived(ago(now, profile.createdAt));
  const tierNote: Record<McpTier, string> = { aggregate: T.tierAggregateNote, evidence: T.tierEvidenceNote };
  const tierLabel: Record<McpTier, string> = { aggregate: T.tierAggregate, evidence: T.tierEvidence };
  const tierOrder: McpTier[] = ['aggregate', 'evidence'];

  function pickTier(tier: McpTier) { onChange(withTier(draft, tiers, tier)); }
</script>

<article class="mcp-card" aria-labelledby={key + '-h'} data-testid="mcp-card" data-profile={profile.id}>
  <h3 id={key + '-h'}>{profile.enrolled ? 'A program wants to read your audit data' : 'An entry waits for a program'}</h3>

  <section class="who" aria-label={T.reportedLabel}>
    <p class="field-note">{T.reportedLabel}</p>
    <dl>
      <dt>{T.nameLabel}</dt><dd data-testid="mcp-host-name">{profile.hostName || '-'}</dd>
      {#if profile.hostVersion}<dt>{T.versionLabel}</dt><dd data-testid="mcp-host-version">{profile.hostVersion}</dd>{/if}
      <dt>{T.pathLabel}</dt><dd class="path" data-testid="mcp-host-exe">{profile.exe || '-'}</dd>
      <dt>{T.idLabel}</dt><dd>{profile.id}</dd>
      {#if seen}<dt>{T.firstSeen}</dt><dd>{seen}</dd>{/if}
    </dl>
    <p class="field-note">{T.pathNote}</p>
  </section>

  {#if !profile.enrolled}
    <div class="notice" role="note"><strong>{T.notConnectedTitle}</strong> {T.notConnectedBody}</div>
  {/if}

  {#if profile.contested > 0}
    <div class="warning" role="alert" data-testid="mcp-contested">
      <strong>{T.contestedTitle}</strong>
      <p>{T.contestedBody(profile.contested)} {T.contestedAdvice}</p>
      <label class="check"><input type="checkbox" checked={draft.acceptContested} disabled={!!busy} onchange={(e) => { onChange({ ...draft, acceptContested: e.currentTarget.checked }); }} /> <span>{T.contestedAccept}</span></label>
    </div>
  {/if}

  {#if profile.enrolled}
    {#if profile.requestedIdentifiers}<p class="field-note">{T.identifiersAsked}</p>{/if}
    <div class="choices">
      <fieldset>
        <legend>{T.tierLegend}</legend>
        {#each tierOrder as tier (tier)}
          <label class="option"><input type="radio" name={key + '-tier'} value={tier} checked={draft.tier === tier} disabled={!!busy} onchange={() => pickTier(tier)} /> <span><strong>{tierLabel[tier]}</strong><span class="field-note">{tierNote[tier]}</span></span></label>
        {/each}
      </fieldset>

      <fieldset>
        <legend>{T.toolsLegend}</legend>
        {#each toolsOf(tiers, draft.tier) as tool (tool)}
          <label class="check"><input type="checkbox" checked={draft.tools.includes(tool)} disabled={!!busy} onchange={(e) => { onChange({ ...draft, tools: toggled(draft.tools, tool, e.currentTarget.checked) }); }} /> <span>{toolLabel(tool)}</span></label>
        {/each}
      </fieldset>

      <fieldset>
        <legend>{T.sourcesLegend}</legend>
        {#if noSources}<p class="field-note">{T.sourcesNone}</p>{/if}
        {#each sources as source (source.id)}
          <label class="check"><input type="checkbox" checked={draft.sources.includes(source.id)} disabled={!!busy} onchange={(e) => { onChange({ ...draft, sources: toggled(draft.sources, source.id, e.currentTarget.checked) }); }} /> <span>{source.label}</span></label>
        {/each}
      </fieldset>

      <fieldset>
        <legend>{T.identifiersLegend}</legend>
        <label class="check"><input type="checkbox" checked={draft.identifiers} disabled={!!busy} onchange={(e) => { onChange({ ...draft, identifiers: e.currentTarget.checked }); }} /> <span>{T.identifiersLabel}</span></label>
        <p class="field-note">{T.identifiersNote}</p>
        {#if draft.identifiers}
          <label class="select"><span>{T.identifiersForLabel}</span>
            <select value={String(draft.identifiersMinutes)} disabled={!!busy} onchange={(e) => { onChange({ ...draft, identifiersMinutes: Number(e.currentTarget.value) }); }}>
              {#each identChoices as minutes (minutes)}<option value={String(minutes)}>{span(minutes)}</option>{/each}
            </select>
          </label>
        {/if}
      </fieldset>

      <fieldset>
        <legend>{T.idleLegend}</legend>
        <label class="select"><span class="vh">{T.idleLegend}</span>
          <select value={String(draft.idleMinutes)} disabled={!!busy} onchange={(e) => { onChange({ ...draft, idleMinutes: Number(e.currentTarget.value) }); }}>
            {#each idleChoices as minutes (minutes)}<option value={String(minutes)}>{span(minutes)}</option>{/each}
          </select>
        </label>
        <p class="field-note">{T.idleNote}</p>
      </fieldset>
    </div>
    <p class="notice hosted" role="note">{T.hostedWarning}</p>
  {/if}

  {#if error}<div class="inline-error" role="alert">{error}</div>{/if}
  {#if profile.enrolled && problem}<p class="field-note why" data-testid="mcp-why">{T.whyNot}: {problem}</p>{/if}

  <div class="actions">
    {#if profile.enrolled}
      <button type="button" class="primary-button" data-testid="mcp-approve" disabled={!!busy || problem !== ''} onclick={onApprove}>{busy === 'approve' ? T.approving : T.approve}</button>
      <button type="button" class="secondary-button" data-testid="mcp-deny" disabled={!!busy} onclick={onDeny}>{busy === 'deny' ? T.denying : T.deny}</button>
      <button type="button" class="secondary-button" data-testid="mcp-startover" disabled={!!busy} onclick={onStartOver}>{busy === 'reissue' ? T.startingOver : T.startOver}</button>
    {:else}
      <button type="button" class="secondary-button" data-testid="mcp-remove" disabled={!!busy} onclick={onRemove}>{busy === 'remove' ? T.removing : T.removeEntry}</button>
    {/if}
  </div>
</article>

<style>
  .mcp-card { display: grid; gap: var(--space-3); min-width: 0; padding: var(--space-3) var(--space-4); background: var(--surface-2); border: 1px solid var(--line); border-left: 3px solid var(--warn); border-radius: var(--radius-sm); }
  h3 { margin: 0; font-size: 15px; font-weight: 600; }
  .who { display: grid; gap: 4px; min-width: 0; }
  dl { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: 3px var(--space-3); margin: 0; font-size: 13px; }
  dt { color: var(--text-2); }
  dd { margin: 0; overflow-wrap: anywhere; }
  .path { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; }
  .field-note { margin: 2px 0 0; }
  .notice { margin: 0; }
  .warning { display: grid; gap: var(--space-2); padding: var(--space-2) var(--space-3); font-size: 13px; background: color-mix(in srgb, var(--bad) 10%, var(--surface)); border: 1px solid var(--bad); border-radius: var(--radius-sm); }
  .warning p { margin: 0; }
  .choices { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--space-3); }
  @media (min-width: 720px) { .choices { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
  fieldset { display: grid; gap: 6px; align-content: start; margin: 0; padding: 0; border: 0; min-width: 0; }
  legend { padding: 0 0 4px; font-size: 13px; font-weight: 600; }
  .check, .option { display: flex; align-items: flex-start; gap: 8px; min-height: 24px; font-size: 13px; }
  .option > span { display: grid; gap: 1px; }
  .check input, .option input { margin-top: 3px; }
  .select { display: grid; gap: 4px; font-size: 13px; }
  select { box-sizing: border-box; min-height: 34px; max-width: 260px; padding: 4px 8px; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: 13px; }
  .vh { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  .hosted { color: var(--text); }
  .why { color: var(--text-2); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>

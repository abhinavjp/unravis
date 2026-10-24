<script lang="ts">
  import { addSource, app, clearCACertificate, clearConnectionDetails, closeConnections, conn, displayLastUsed, forgetRecent, loadCACertificate, loadRecent, panels, redactedAddress, saveConnectionDetails, selectAllDatabases, setDatabaseSelected, startNewConnection, testConnection } from '../lib/appState.svelte';
  import Drawer from './Drawer.svelte';

  // The Connections drawer: saved connection settings, and the form that tests a MongoDB connection and adds its databases as sources.
  // Esc is not stopped here, as before: the page closes the drawer and hands focus back to the button that opened it.
</script>

<Drawer title="Connections" titleId="connections-title" intro="Saved settings and database sources" closeLabel="Close connections" focusable={'button:not(:disabled), input:not(:disabled), select:not(:disabled), [href], [tabindex]:not([tabindex="-1"])'} stopEscape={false} bind:element={panels.drawerElement} onClose={closeConnections}>
  <div class="connection-tabs"><button class:active={panels.connectionTab === 'saved'} disabled={conn.sourceBusy || conn.connectionTesting || conn.historyMutationBusy} onclick={() => panels.connectionTab = 'saved'}>Saved connections</button><button class:active={panels.connectionTab === 'new'} disabled={conn.sourceBusy || conn.connectionTesting || conn.historyMutationBusy} onclick={startNewConnection}>New connection</button></div>
  {#if panels.connectionTab === 'saved'}
    <div class="saved-connections-panel">
      {#if conn.recentConnections.length}{#each conn.recentConnections as recent (recent.id)}
        <article class="recent-card"><div class="recent-card-copy"><strong>{recent.label}</strong><div class="recent-username"><span>Username</span><b>{recent.username || 'Not provided'}</b></div><small>{redactedAddress(recent.uri)}</small><small>{recent.tlsMode === 'system' ? 'TLS · Verified certificate' : recent.tlsMode === 'custom' ? 'TLS · Certificate file' : recent.tlsMode === 'insecure' ? 'TLS · Verification skipped' : recent.tlsMode === 'off' ? 'TLS off' : 'TLS · From address'} · {recent.collection || 'ApiAuditLog'}</small><small>Saved {displayLastUsed(recent.lastUsedAt)}</small>{#if recent.hasCredentials}<span class="saved-badge">Password saved on this account</span>{/if}</div><div class="recent-card-actions"><button class="quiet-button" disabled={conn.sourceBusy || conn.connectionTesting || conn.historyMutationBusy} onclick={() => { if (conn.sourceBusy || conn.connectionTesting || conn.historyMutationBusy) return; clearConnectionDetails(); loadRecent(recent); panels.connectionTab = 'new'; }}>{conn.sourceBusy || conn.connectionTesting || conn.historyMutationBusy ? 'Working…' : 'Use settings'}</button><button class="quiet-button" onclick={() => void forgetRecent(recent)} disabled={conn.sourceBusy || conn.connectionTesting || conn.historyMutationBusy || conn.recentBusyId === recent.id}>{conn.recentBusyId === recent.id ? 'Removing…' : 'Forget'}</button></div></article>
      {/each}{:else}<div class="drawer-empty"><strong>No saved connections</strong><p>Save connection settings to load them here later.</p><button class="primary-button" onclick={startNewConnection}>Create a connection</button></div>{/if}
      {#if conn.recentHistoryError}<p class="connection-status warning" role="status">{conn.recentHistoryError}</p>{/if}
      {#if conn.recentSaveError}<p class="connection-status warning" role="status">{#if conn.recentSaveErrorStage}<span class="error-stage">{conn.recentSaveErrorStage}</span>{/if}{conn.recentSaveError}</p>{/if}
      {#if conn.recentSaveSuccess}<p class="connection-status" role="status">{conn.recentSaveSuccess}</p>{/if}
    </div>
  {:else}
  {#if !app.info?.demo}
  <form class="source-form" onsubmit={addSource}>
    {#if conn.sourceError}<div class="inline-error" role="alert">{conn.sourceError}</div>{/if}
    {#if conn.sourceSuccess}<p class="connection-status" role="status">{conn.sourceSuccess}</p>{/if}
    {#if conn.recentSaveError}<p class="connection-status warning" role="status">{#if conn.recentSaveErrorStage}<span class="error-stage">{conn.recentSaveErrorStage}</span>{/if}{conn.recentSaveError}</p>{/if}
    {#if conn.recentSaveSuccess}<p class="connection-status" role="status">{conn.recentSaveSuccess}</p>{/if}
    <fieldset class="connection-section">
    <legend>Connection</legend>
    <label for="source-label">Connection name</label><input id="source-label" bind:value={conn.addLabel} placeholder="UK production" autocomplete="off" disabled={conn.sourceBusy} />
    <label for="source-uri">Server / connection address</label><input id="source-uri" bind:value={conn.addUri} type="text" placeholder="uk-01-mongo.brainpayroll.co.uk" autocomplete="off" disabled={conn.sourceBusy} />
    <p class="field-note">Enter a host name (uses MongoDB port 27017) or a full mongodb:// or mongodb+srv:// address.</p>
    </fieldset>
    <fieldset class="connection-section">
    <legend>Authentication</legend>
    <label for="source-username">Username</label><input id="source-username" bind:value={conn.addUsername} type="text" autocomplete="username" disabled={conn.sourceBusy} />
    <label for="source-password">Password</label><input id="source-password" bind:value={conn.addPassword} type="password" autocomplete="new-password" disabled={conn.sourceBusy} />
    {#if conn.savedCredentialAvailable && conn.savedConnectionId && !conn.addPassword}
      <p class="field-note saved-password-note" role="status">
        {conn.savedCredentialSessionOnly ? 'Password is available for this session only; it is not saved to this Windows account.' : 'A saved password will be used. The server never returns it to this page.'}
      </p>
    {:else if conn.savedConnectionId && !conn.addPassword}
      <p class="field-note" role="status">This recent has no saved password. Enter it again before testing; the server never returns passwords to this page.</p>
    {/if}
    <details class="auth-options">
      <summary>Authentication database</summary>
      <label for="source-auth-source">Authentication database</label><input id="source-auth-source" bind:value={conn.addAuthSource} placeholder="URI setting or admin" autocomplete="off" disabled={conn.sourceBusy} />
      <p class="field-note">Where this user account was created. It can differ from the audit database you select after connecting.</p>
    </details>
    </fieldset>
    <fieldset class="connection-section">
    <legend>SSL / TLS</legend>
    <label for="source-tls">Encryption and server trust</label>
    <select id="source-tls" bind:value={conn.addTLSMode} disabled={conn.sourceBusy}>
      <option value="system">On — verify certificate</option>
      <option value="custom">On — use certificate file</option>
      <option value="insecure">On — skip verification (advanced)</option>
      <option value="uri">From connection URI</option>
      <option value="off">Off</option>
    </select>
    {#if conn.addTLSMode === 'system'}<p class="field-note">Uses Windows trusted certificates to verify the server and encrypt traffic.</p>{/if}
    {#if conn.addTLSMode === 'custom'}
      <p class="field-note">For a private CA or self-signed server: import its public certificate (.pem/.crt). This verifies the server; it is not a client certificate or private key.</p>
      <label for="source-ca-file">Certificate authority file</label>
      <input id="source-ca-file" bind:this={conn.caCertificateInput} type="file" accept=".pem,.crt,application/x-pem-file" onchange={loadCACertificate} disabled={conn.sourceBusy} />
      {#if conn.caCertificateName}<div class="certificate-file"><span>{conn.caCertificateName} · {Math.ceil(new TextEncoder().encode(conn.addCACertificate).length / 1024)} KiB</span><button class="quiet-button" type="button" onclick={clearCACertificate} disabled={conn.sourceBusy}>Clear certificate</button></div>{/if}
    {/if}
    {#if conn.addTLSMode === 'insecure'}<div class="tls-insecure-warning" role="alert"><strong>Certificate verification is disabled.</strong> Traffic remains encrypted, but the server certificate and host name are not checked. Accepts self-signed and other unverified certificates. Use only for a trusted network or temporary diagnosis.</div>{/if}
    {#if conn.addTLSMode === 'off'}<p class="field-note">Traffic is not encrypted. Use only when the server is on a trusted local network.</p>{/if}
    </fieldset>
    <div class="remember-password-control">
      <label><input type="checkbox" bind:checked={conn.rememberPassword} disabled={conn.sourceBusy || conn.connectionTesting || conn.historyMutationBusy} /> <span>Save password on this Windows account</span></label>
      <p class="field-note">Applied when you save these details. You can change it before saving. Passwords are encrypted for the current Windows user when saved.</p>
    </div>
    <button class="secondary-button full-button" type="button" onclick={testConnection} disabled={conn.connectionTesting || conn.sourceBusy || conn.historyMutationBusy || !conn.addUri.trim()}>{conn.connectionTesting ? 'Testing connection…' : 'Test connection & load databases'}</button>
    <button class="quiet-button full-button save-connection-button" type="button" onclick={saveConnectionDetails} disabled={conn.connectionTesting || conn.sourceBusy || conn.historyMutationBusy || conn.forgetInProgress || !conn.addUri.trim() || !conn.addCollection.trim() || (conn.addTLSMode === 'custom' && !conn.addCACertificate)}>{conn.historyMutationBusy ? 'Saving connection…' : 'Save connection'}</button>
    <p class="field-note">You can save these details even if the server is unavailable. Saving does not test or connect to the database.</p>
    <button class="quiet-button clear-connection" type="button" onclick={clearConnectionDetails} disabled={conn.sourceBusy || (!conn.addUri && !conn.addUsername && !conn.addPassword && !conn.addAuthSource && conn.addTLSMode === 'system' && !conn.addCACertificate && !conn.savedConnectionId && !conn.discoveredDatabases.length)}>Clear connection details</button>
    {#if conn.connectionTesting}<p class="connection-status" role="status">Testing credentials and discovering databases…</p>{:else if conn.historyMutationBusy}<p class="connection-status" role="status">Updating recent connection…</p>{/if}
    {#if conn.connectionTestedSignature === conn.connectionSignature && !conn.manualDatabase}
      {#if conn.discoveredDatabases.length}
        <section class="database-discovery" aria-label="Available databases">
          <div class="database-heading"><strong>Select databases</strong><span>{conn.selectedDatabases.length} selected · {app.remainingSourceSlots} profile slots remaining</span></div>
          <div class="database-actions"><button type="button" class="quiet-button" onclick={selectAllDatabases} disabled={conn.sourceBusy || app.remainingSourceSlots === 0}>Select all</button><button type="button" class="quiet-button" onclick={() => { conn.selectedDatabases = []; conn.sourceError = ''; conn.sourceSuccess = ''; }} disabled={conn.sourceBusy || !conn.selectedDatabases.length}>Clear selection</button></div>
          <div class="database-checks">
            {#each conn.availableDatabases as database}
              <label class="database-option"><input type="checkbox" checked={conn.selectedDatabases.includes(database)} disabled={conn.sourceBusy || (!conn.selectedDatabases.includes(database) && conn.selectedDatabases.length >= app.remainingSourceSlots)} onchange={(event) => setDatabaseSelected(database, event.currentTarget.checked)} /><span>{database}</span></label>
            {/each}
          </div>
          {#if conn.completedDatabases.length}<p class="field-note">{conn.completedDatabases.length} database{conn.completedDatabases.length === 1 ? ' was' : 's were'} already added from this connection and removed from the available choices.</p>{/if}
          {#if conn.availableDatabases.length > app.remainingSourceSlots}<p class="field-note">This utility supports up to {app.maxSources} source profiles. Select up to the {app.remainingSourceSlots} remaining slot{app.remainingSourceSlots === 1 ? '' : 's'}; you can add the other databases later.</p>{/if}
        </section>
      {:else}
        <p class="connection-status" role="status">Connection succeeded, but the server returned no database names.</p>
      {/if}
      {#if conn.connectionWarning}<p class="connection-status warning" role="status">{conn.connectionWarning}</p>{/if}
    {/if}
    {#if conn.connectionError}<div class="inline-error" role="alert">{#if conn.connectionErrorStage}<span class="error-stage">{conn.connectionErrorStage}</span>{/if}{conn.connectionErrorContext ? `${conn.connectionErrorContext}: ` : ''}{conn.connectionError}</div>{/if}
    <button class="quiet-button manual-database-toggle" type="button" aria-expanded={conn.manualDatabase} disabled={conn.sourceBusy} onclick={() => { conn.manualDatabase = !conn.manualDatabase; conn.manualDatabaseName = ''; conn.selectedDatabases = []; }}>
      {conn.manualDatabase ? 'Choose a discovered database instead' : 'Enter database manually'}
    </button>
    {#if conn.manualDatabase}
      <label for="source-database-manual">Database name</label><input id="source-database-manual" bind:value={conn.manualDatabaseName} placeholder="payroll" autocomplete="off" disabled={conn.sourceBusy} />
      <p class="field-note">Use this when your account can read a database but cannot list database names. Saving still verifies access.</p>
    {/if}
    <label for="source-collection">Audit log collection</label><input id="source-collection" bind:value={conn.addCollection} placeholder="ApiAuditLog" autocomplete="off" disabled={conn.sourceBusy} />
    <p class="field-note">Exact collection where API logs are stored. Defaults to ApiAuditLog.</p>
    <button class="primary-button full-button" type="submit" disabled={conn.sourceBusy || conn.connectionTesting || conn.historyMutationBusy || (conn.addTLSMode === 'custom' && !conn.addCACertificate) || app.remainingSourceSlots === 0 || !conn.addLabel.trim() || !conn.addUri.trim() || !(conn.manualDatabase ? conn.manualDatabaseName.trim() : conn.selectedDatabases.length) || !conn.addCollection.trim() || (!conn.manualDatabase && conn.connectionTestedSignature !== conn.connectionSignature)}>{conn.sourceBusy ? 'Adding databases…' : conn.manualDatabase ? 'Add database' : 'Add selected databases'}</button>
    <p class="field-note">Credentials are used to test and save each database profile; they are not shown in the source list.</p>
  </form>
  {/if}
  {/if}
</Drawer>

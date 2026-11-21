<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { getSignInSettings, linkClerk, setClerkKey, setSignInMethods, unlinkClerk, type SignInSettings } from './api';
  import { loadClerk, type ClerkClient } from './clerk';
  import { endsThisSession, keyHost, methodsProblem, methodsSummary } from './signin';

  let data: SignInSettings | null = null;
  let panelToken = true;
  let clerkOn = false;
  let keyInput = '';
  let busy = false;
  let error = '';
  let notice = '';
  let clerk: ClerkClient | undefined;
  let stopListening: (() => void) | undefined;
  // Remembers, across Clerk's sign-in redirect, that this page asked to link.
  const linkingKey = 'telemetry-clerk-linking';

  onMount(() => { void load(true); });
  onDestroy(() => stopListening?.());

  async function load(resume = false) {
    try {
      data = await getSignInSettings();
      panelToken = data.panel_token.enabled;
      clerkOn = data.clerk.enabled;
      if (resume && wantsLink() && data.clerk.publishable_key && !data.clerk.linked) await startLink();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load sign-in settings.';
    }
  }

  function wantsLink(): boolean {
    try { return sessionStorage.getItem(linkingKey) === '1'; } catch { return false; }
  }

  function setWantsLink(value: boolean) {
    try { if (value) sessionStorage.setItem(linkingKey, '1'); else sessionStorage.removeItem(linkingKey); } catch {}
  }

  async function act(work: () => Promise<string>) {
    busy = true;
    error = '';
    notice = '';
    try {
      notice = await work();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Something went wrong.';
    } finally {
      busy = false;
    }
  }

  const saveMethods = () => act(async () => {
    if (!data) return '';
    const endsSession = endsThisSession(data, panelToken);
    data = await setSignInMethods(panelToken, clerkOn);
    if (endsSession) {
      // This panel-token session no longer counts; show the sign-in screen.
      window.location.reload();
      return 'Saved. Sign in with Clerk.';
    }
    return 'Saved.';
  });

  const saveKey = () => act(async () => {
    data = await setClerkKey(keyInput.trim());
    keyInput = '';
    clerkOn = data.clerk.enabled;
    return 'Key saved. Now sign in to Clerk to link your account.';
  });

  // Loads Clerk for the saved key and links the account signed in there.
  async function startLink() {
    if (!data?.clerk.publishable_key) return;
    await act(async () => {
      clerk = await loadClerk(data!.clerk.publishable_key!);
      if (clerk.isSignedIn) return finishLink();
      setWantsLink(true);
      stopListening?.();
      stopListening = clerk.addListener?.(() => { if (clerk?.isSignedIn && wantsLink()) void act(finishLink); });
      await clerk.openSignIn({ redirectUrl: window.location.href });
      return '';
    });
  }

  async function finishLink(): Promise<string> {
    setWantsLink(false);
    stopListening?.();
    stopListening = undefined;
    const token = await clerk?.session?.getToken();
    if (!token) throw new Error('Clerk did not return a session. Sign in again.');
    data = await linkClerk(token);
    clerkOn = data.clerk.enabled;
    return `Linked ${data.clerk.owner_user_id}. Turn Clerk on below when you are ready.`;
  }

  async function relink() {
    // Sign out of Clerk first so another account can be chosen.
    await act(async () => {
      if (!data?.clerk.publishable_key) return '';
      clerk = await loadClerk(data.clerk.publishable_key);
      if (clerk.isSignedIn) await clerk.signOut();
      return '';
    });
    await startLink();
  }

  const remove = () => act(async () => {
    data = await unlinkClerk();
    clerkOn = false;
    return 'Clerk removed.';
  });

  $: problem = data ? methodsProblem(data, panelToken, clerkOn) : '';
  $: changed = data ? panelToken !== data.panel_token.enabled || clerkOn !== data.clerk.enabled : false;
  $: pastedHost = keyHost(keyInput);
  const when = (at: string) => new Date(at).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });
</script>

<section class="sign-in" aria-labelledby="sign-in-heading">
  <h2 id="sign-in-heading">Sign-in</h2>
  {#if !data}
    <p class="muted">{error || 'Loading…'}</p>
  {:else if data.managed_by === 'environment'}
    <p>Sign-in is set by this server's configuration.</p>
    <p class="muted small">Clerk {data.clerk.enabled ? 'is on' : 'is set up, but no owner is configured'}{data.clerk.issuer ? ` (${data.clerk.issuer})` : ''}. The panel token is off. Change <code>TELEMETRY_CLERK_*</code> in the server's environment to change this.</p>
  {:else}
    <p class="muted small">{methodsSummary(data)}</p>
    {#if data.blocked}<p class="error" role="alert">{data.blocked}</p>{/if}

    <form class="block" on:submit|preventDefault={saveMethods}>
      <h3>Methods</h3>
      <label class="row"><input type="checkbox" bind:checked={panelToken} disabled={busy || !data.can_change} /> Panel token
        {#if !data.panel_token.configured}<span class="muted small">— not set on this server (TELEMETRY_PANEL_TOKEN)</span>{/if}</label>
      <label class="row"><input type="checkbox" bind:checked={clerkOn} disabled={busy || !data.can_change || !data.clerk.linked} /> Clerk
        {#if !data.clerk.linked}<span class="muted small">— link an account below first</span>{/if}</label>
      {#if changed && endsThisSession(data, panelToken)}<p class="muted small">Turning the panel token off signs this session out. You will sign in with Clerk. If you get locked out, run <code>telemetryd -reset-sign-in</code> on the server.</p>{/if}
      {#if problem}<p class="muted small">{problem}</p>{/if}
      <button disabled={busy || !data.can_change || !changed || Boolean(problem)}>Save</button>
    </form>

    <div class="block">
      <h3>Clerk</h3>
      {#if data.clerk.linked}
        <p class="small">Linked to <code>{data.clerk.owner_user_id}</code> in <code>{data.clerk.issuer}</code>{data.clerk.development ? ' (development instance)' : ''}.</p>
        <div class="row">
          <button class="quiet-button" disabled={busy || !data.can_change} on:click={relink}>Link another account</button>
          <button class="quiet-button" disabled={busy || !data.can_change || !data.panel_token.enabled} on:click={remove}>Remove Clerk</button>
        </div>
        {#if !data.panel_token.enabled}<p class="muted small">Turn the panel token on before removing Clerk.</p>{/if}
      {:else if data.clerk.publishable_key}
        <p class="small">Key saved for <code>{data.clerk.issuer}</code>. Sign in to Clerk once so this server knows your account.</p>
        <div class="row">
          <button disabled={busy || !data.can_change} on:click={startLink}>Sign in to Clerk and link</button>
          <button class="quiet-button" disabled={busy || !data.can_change} on:click={remove}>Remove key</button>
        </div>
      {/if}

      <form class="row" on:submit|preventDefault={saveKey}>
        <label>Publishable key <input class="key" bind:value={keyInput} placeholder="pk_test_… or pk_live_…" autocomplete="off" spellcheck="false" disabled={busy || !data.can_change} /></label>
        <button disabled={busy || !data.can_change || !pastedHost}>{data.clerk.publishable_key ? 'Replace key' : 'Save key'}</button>
      </form>
      {#if keyInput && !pastedHost}<p class="muted small">That is not a Clerk publishable key. It starts with <code>pk_test_</code> or <code>pk_live_</code>; never paste the secret key (<code>sk_…</code>).</p>{/if}
      {#if data.clerk.enabled && keyInput}<p class="muted small">A new key turns Clerk off until you link again.</p>{/if}

      <details>
        <summary>What you need from Clerk</summary>
        <ol class="small">
          <li>Make a free account at <a href="https://dashboard.clerk.com" target="_blank" rel="noreferrer">dashboard.clerk.com</a> and create an application. Turn on the sign-in methods you want, such as email code or Google.</li>
          <li>A <strong>Development</strong> instance (key starts <code>pk_test_</code>) is enough for one person. A <strong>Production</strong> instance needs a domain you own and DNS records on it, so it does not work on a <code>*.ts.net</code> address.</li>
          <li>Copy the <strong>Publishable key</strong> from the <a href="https://dashboard.clerk.com/~/api-keys" target="_blank" rel="noreferrer">API keys</a> page and paste it above. Never paste the secret key.</li>
          <li>Open this panel at <code>{data.public_url || 'TELEMETRY_PUBLIC_URL'}</code> when you sign in. Clerk stamps that address into each sign-in, and this server only accepts sign-ins made from it.</li>
          <li>Press <em>Sign in to Clerk and link</em>, sign in, then turn Clerk on under Methods.</li>
        </ol>
        <p class="muted small">Clerk sees only sign-in events: your account, when you sign in and from which browser. Your Telemetry data stays on this server and never goes to Clerk.</p>
      </details>
    </div>

    {#if data.changes.length}
      <div class="block">
        <h3>Changes</h3>
        <ul class="changes small">
          {#each data.changes as item}<li><span class="muted">{when(item.changed_at)}</span> {item.change}</li>{/each}
        </ul>
      </div>
    {/if}
  {/if}
  {#if data && error}<p class="error" role="alert">{error}</p>{/if}
  {#if notice}<p class="muted small" role="status">{notice}</p>{/if}
</section>

<style>
  .sign-in { border: 1px solid var(--fg); background: var(--panel); margin: 16px 0; padding: 12px; }
  h2 { margin: 0 0 6px; font-size: 1.05rem; }
  h3 { margin: 0 0 6px; font-size: .92rem; }
  .block { border-top: 1px solid var(--line); margin-top: 12px; padding-top: 10px; }
  .row { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; margin: 6px 0; font-size: .85rem; }
  .key { width: min(34em, 100%); }
  label .key { margin-left: 6px; }
  details { margin-top: 10px; }
  summary { cursor: pointer; font-size: .85rem; }
  ol { line-height: 1.6; padding-left: 1.4em; }
  a { color: var(--fg); }
  code { overflow-wrap: anywhere; }
  .changes { list-style: none; margin: 0; padding: 0; line-height: 1.6; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  .error { color: var(--fg); font-weight: 700; font-size: .82rem; }
</style>

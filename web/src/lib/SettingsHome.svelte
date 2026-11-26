<script lang="ts">
  import { getLock, getPolicy, getSignInSettings, listDevices, listSources, type Device, type LockResponse, type PolicyConfig, type SignInSettings, type SourceSummary } from './api';
  import { phoneLinks, settingsPages, type SettingsPage } from './nav';
  import { budgetValue, devicesValue, distractionsValue, lockValue, signInValue, sourcesValue, timesValue } from './settings';

  // Every setting in one grouped list. Each row shows a short current value
  // and opens its page. Inside the phone app the phone's own settings come
  // first and sign-in is left to the app.
  export let inApp = false;
  export let themeLabel = '';
  export let oncycletheme: () => void = () => {};
  export let onsignout: () => void = () => {};

  let config: PolicyConfig | undefined;
  let lock: LockResponse | undefined;
  let devices: Device[] | undefined;
  let sources: SourceSummary[] | undefined;
  let signIn: SignInSettings | undefined;

  // Each value loads on its own; one that fails leaves its row without a value.
  void getPolicy().then(policy => { config = policy?.config ?? { distractions: [], essentials: [] }; }).catch(() => undefined);
  void getLock().then(value => { lock = value; }).catch(() => undefined);
  void listDevices().then(value => { devices = value; }).catch(() => undefined);
  void listSources().then(value => { sources = value; }).catch(() => undefined);
  $: if (!inApp && !signIn) void getSignInSettings().then(value => { signIn = value; }).catch(() => undefined);

  type Row = { page: SettingsPage; value: string };
  $: blocking = [
    { page: 'budget', value: budgetValue(config) },
    { page: 'times', value: timesValue(config, lock) },
    { page: 'distractions', value: distractionsValue(config) },
    { page: 'lock', value: lockValue(lock) }
  ] as Row[];
  $: system = [
    { page: 'devices', value: devicesValue(devices) },
    { page: 'sources', value: sourcesValue(sources) },
    ...(inApp ? [] : [{ page: 'sign-in', value: signInValue(signIn) }])
  ] as Row[];
</script>

<div class="settings-home">
  <section aria-labelledby="group-blocking">
    <h2 id="group-blocking" class="group">Blocking</h2>
    <ul class="rows">
      {#each blocking as row (row.page)}
        <li><a href="#settings/{row.page}"><span class="label">{settingsPages[row.page]}</span><span class="value">{row.value}</span><span class="chevron" aria-hidden="true">›</span></a></li>
      {/each}
    </ul>
  </section>

  {#if inApp}
    <section aria-labelledby="group-phone">
      <h2 id="group-phone" class="group">This phone</h2>
      <ul class="rows">
        <li><a href={phoneLinks.permissions}><span class="label">Permissions</span><span class="value"></span><span class="chevron" aria-hidden="true">›</span></a></li>
        <li><a href={phoneLinks.notifications}><span class="label">Notifications</span><span class="value"></span><span class="chevron" aria-hidden="true">›</span></a></li>
      </ul>
    </section>
  {/if}

  <section aria-labelledby="group-system">
    <h2 id="group-system" class="group">System</h2>
    <ul class="rows">
      {#each system as row (row.page)}
        <li><a href="#settings/{row.page}"><span class="label">{settingsPages[row.page]}</span><span class="value">{row.value}</span><span class="chevron" aria-hidden="true">›</span></a></li>
      {/each}
      <li><button type="button" on:click={oncycletheme}><span class="label">Theme</span><span class="value">{themeLabel}</span><span class="chevron" aria-hidden="true">↻</span></button></li>
    </ul>
  </section>

  {#if !inApp}
    <button type="button" class="quiet-button sign-out" on:click={onsignout}>Sign out</button>
  {/if}
</div>

<style>
  .settings-home { max-width: 640px; }
  .group { color: var(--muted); font-size: .72rem; font-weight: 700; letter-spacing: .08em; margin: 18px 0 6px; text-transform: uppercase; }
  section:first-child .group { margin-top: 0; }
  .rows { background: var(--panel); border: 1px solid var(--fg); list-style: none; margin: 0; padding: 0; }
  .rows li + li { border-top: 1px solid var(--line); }
  .rows a, .rows button {
    align-items: center; background: transparent; border: 0; color: var(--fg); cursor: pointer; display: flex; font: inherit; font-size: .9rem;
    gap: 10px; min-height: 50px; padding: 0 14px; text-align: left; text-decoration: none; width: 100%;
  }
  .rows a:hover, .rows a:focus-visible, .rows button:hover, .rows button:focus-visible { background: var(--soft); color: var(--fg); outline: 0; }
  .rows a:focus-visible, .rows button:focus-visible { box-shadow: inset 0 0 0 2px var(--fg); }
  .label { flex: 1 1 auto; min-width: 0; }
  .value { color: var(--muted); flex: 0 1 auto; font-size: .78rem; max-width: 45%; overflow: hidden; text-align: right; text-overflow: ellipsis; white-space: nowrap; }
  .chevron { color: var(--muted); flex: none; font-size: 1.1rem; width: 1ch; }
  .sign-out { margin-top: 20px; min-height: 44px; }
</style>

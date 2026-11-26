<script lang="ts">
  import type { Device } from './api';
  import AddDevice from './AddDevice.svelte';

  // Settings › Devices: add a device with a setup code, and the connected
  // ones, each removable. The page loads the list.
  export let devices: Device[] = [];
  export let loading = false;
  export let error = '';
  export let onrefresh: () => void;
  export let onrevoke: (device: Device) => void;
</script>

<AddDevice onconnected={onrefresh} />
<section class="card devices" aria-labelledby="devices-heading">
  <div class="card-head">
    <h2 id="devices-heading" class="card-label">Connected devices</h2>
    <button class="quiet-button" on:click={onrefresh} disabled={loading}>{loading ? 'Refreshing…' : 'Refresh'}</button>
  </div>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  <div class="device-list">
    {#each devices as device (device.device_id)}
      <div class:revoked={device.revoked_at} class="device-row">
        <div><strong>{device.device_name}</strong><span>{device.device_id}</span></div>
        {#if device.revoked_at}<span class="muted">Removed · add it again with a setup code</span>{:else}<button class="quiet-button" on:click={() => onrevoke(device)}>Remove</button>{/if}
      </div>
    {:else}<p class="muted empty">{loading ? 'Loading…' : 'No devices'}</p>{/each}
  </div>
</section>

<style>
  .devices { margin-top: 12px; }
  .device-list { border-top: 1px solid var(--line); margin-top: 8px; }
  .device-row { align-items: center; border-bottom: 1px solid var(--line); display: flex; gap: 12px; justify-content: space-between; min-height: 52px; padding: 8px 0; }
  .device-row:last-child { border-bottom: 0; }
  .device-row > div { display: grid; gap: 3px; min-width: 0; }
  .device-row span { color: var(--muted); font-size: .78rem; overflow-wrap: anywhere; }
  .device-row.revoked { opacity: .55; }
  .empty { padding: 12px 0; }
</style>

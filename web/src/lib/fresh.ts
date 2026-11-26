import { onDestroy } from 'svelte';
import { onFresh } from './api';

/**
 * Loads again when a cached answer changed in the background, so a view
 * shown at once from the cache picks up the server's newer answer. Changes
 * that arrive together load once. Call it while the component starts.
 */
export function whenFresh(reload: () => unknown, wait = 50): void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const stop = onFresh(() => {
    if (timer) return;
    timer = setTimeout(() => {
      timer = undefined;
      void reload();
    }, wait);
  });
  onDestroy(() => {
    stop();
    if (timer) clearTimeout(timer);
  });
}

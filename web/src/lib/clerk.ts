/**
 * Small runtime wrapper around ClerkJS.
 *
 * The panel is served as a static Svelte bundle, so Clerk is loaded from the
 * tenant's Frontend API at runtime. The publishable key comes from the
 * authenticated server config endpoint; it is intentionally not compiled
 * into this repository.
 */

export type ClerkSession = {
  getToken: (options?: { template?: string }) => Promise<string | null>;
};

export type ClerkEmission = {
  session?: ClerkSession | null;
  user?: ClerkUser | null;
};

export type ClerkUser = { id: string };

export type ClerkClient = {
  isLoaded?: boolean;
  isSignedIn?: boolean;
  user?: ClerkUser | null;
  session?: ClerkSession | null;
  load: (options?: { ui?: { ClerkUI?: unknown } }) => Promise<void>;
  openSignIn: (options?: { redirectUrl?: string }) => Promise<void>;
  signOut: () => Promise<void>;
  addListener?: (callback: (emission: ClerkEmission) => void, options?: { skipInitialEmit?: boolean }) => () => void;
};

type ClerkWindow = Window & {
  Clerk?: ClerkClient;
  __internal_ClerkUICtor?: unknown;
};

const scriptLoads = new Map<string, Promise<void>>();
let activePublishableKey = '';
let activeClerk: ClerkClient | undefined;

function clerkDomain(publishableKey: string): string {
  const encoded = publishableKey.split('_')[2];
  if (!encoded) throw new Error('Clerk publishable key is malformed.');
  const decoded = window.atob(encoded);
  const domain = decoded.endsWith('$') ? decoded.slice(0, -1) : decoded;
  if (!domain || !/^[a-z0-9.-]+$/i.test(domain)) throw new Error('Clerk Frontend API domain is invalid.');
  return domain;
}

function loadScript(src: string, configure?: (script: HTMLScriptElement) => void): Promise<void> {
  const existing = scriptLoads.get(src);
  if (existing) return existing;
  const promise = new Promise<void>((resolve, reject) => {
    const script = document.querySelector<HTMLScriptElement>(`script[src="${src}"]`) ?? document.createElement('script');
    script.src = src;
    script.async = true;
    script.crossOrigin = 'anonymous';
    configure?.(script);
    script.addEventListener('load', () => resolve(), { once: true });
    script.addEventListener('error', () => reject(new Error('Unable to load Clerk. Check the server CSP and Tailscale connection.')), { once: true });
    if (!script.parentNode) document.head.appendChild(script);
  });
  scriptLoads.set(src, promise);
  return promise;
}

export async function loadClerk(publishableKey: string): Promise<ClerkClient> {
  if (activeClerk && activePublishableKey === publishableKey) return activeClerk;
  const domain = clerkDomain(publishableKey);
  const uiURL = `https://${domain}/npm/@clerk/ui@1/dist/ui.browser.js`;
  const clerkURL = `https://${domain}/npm/@clerk/clerk-js@5/dist/clerk.browser.js`;
  await loadScript(uiURL);
  await loadScript(clerkURL, script => { script.dataset.clerkPublishableKey = publishableKey; });
  const clerk = (window as ClerkWindow).Clerk;
  if (!clerk) throw new Error('Clerk loaded without its browser client.');
  await clerk.load({ ui: { ClerkUI: (window as ClerkWindow).__internal_ClerkUICtor } });
  activePublishableKey = publishableKey;
  activeClerk = clerk;
  return clerk;
}

export function clearClerk(): void {
  activePublishableKey = '';
  activeClerk = undefined;
}

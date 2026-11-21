import type { SignInSettings } from './api';

/**
 * Why a choice of sign-in methods would be refused, or '' when it is fine.
 * The server enforces the same rules; this only explains them before saving.
 */
export function methodsProblem(settings: SignInSettings, panelToken: boolean, clerk: boolean): string {
  if (clerk && !settings.clerk.linked) return 'Link a Clerk account before turning Clerk on.';
  if (!panelToken && !clerk) return 'Keep at least one way to sign in.';
  return '';
}

/** The Clerk Frontend API host inside a publishable key, or '' if it is not one. */
export function keyHost(key: string): string {
  const match = /^pk_(test|live)_([A-Za-z0-9+/=]+)$/.exec(key.trim());
  if (!match) return '';
  try {
    const decoded = atob(match[2]);
    return decoded.endsWith('$') && /^[a-z0-9.-]+\.[a-z0-9-]+$/i.test(decoded.slice(0, -1)) ? decoded.slice(0, -1) : '';
  } catch {
    return '';
  }
}

/** A short line for what is on now. */
export function methodsSummary(settings: SignInSettings): string {
  const on = [settings.panel_token.enabled ? 'panel token' : '', settings.clerk.enabled ? 'Clerk' : ''].filter(Boolean);
  return on.length ? `Signing in with ${on.join(' or ')}.` : 'No sign-in method is on.';
}

/** Whether saving these methods ends the current panel-token session. */
export function endsThisSession(settings: SignInSettings, panelToken: boolean): boolean {
  return settings.panel_token.enabled && !panelToken;
}

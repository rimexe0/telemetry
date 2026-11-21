import { describe, expect, it } from 'vitest';
import type { SignInSettings } from './api';
import { endsThisSession, keyHost, methodsProblem, methodsSummary } from './signin';

const base: SignInSettings = {
  managed_by: 'settings', public_url: 'https://t.example.ts.net:8443', can_change: true,
  panel_token: { enabled: true, configured: true },
  clerk: { enabled: false, linked: false, development: true }, changes: []
};
const linked: SignInSettings = { ...base, clerk: { ...base.clerk, linked: true, owner_user_id: 'user_1' } };

describe('sign-in methods', () => {
  it('explains the rules the server enforces', () => {
    expect(methodsProblem(base, true, true)).toMatch(/Link a Clerk account/);
    expect(methodsProblem(linked, false, false)).toMatch(/at least one/);
    expect(methodsProblem(linked, false, true)).toBe('');
    expect(methodsProblem(base, true, false)).toBe('');
  });

  it('summarises what is on', () => {
    expect(methodsSummary(base)).toBe('Signing in with panel token.');
    expect(methodsSummary({ ...linked, clerk: { ...linked.clerk, enabled: true } })).toBe('Signing in with panel token or Clerk.');
  });

  it('warns when the panel token session will end', () => {
    expect(endsThisSession(base, false)).toBe(true);
    expect(endsThisSession(base, true)).toBe(false);
  });
});

describe('publishable keys', () => {
  it('reads the Frontend API host', () => {
    expect(keyHost('pk_test_' + btoa('fond-owl-12.clerk.accounts.dev$'))).toBe('fond-owl-12.clerk.accounts.dev');
    expect(keyHost(' pk_live_' + btoa('clerk.example.com$') + ' ')).toBe('clerk.example.com');
    expect(keyHost('sk_test_' + btoa('clerk.example.com$'))).toBe('');
    expect(keyHost('pk_test_' + btoa('clerk.example.com'))).toBe('');
    expect(keyHost('pk_test_%%')).toBe('');
  });
});

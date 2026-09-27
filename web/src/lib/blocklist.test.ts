import { expect, test } from 'vitest';
import { packagesOf, setPackage } from './blocklist';
import type { PolicyConfig } from './api';

test('a package lives in at most one list and can be removed', () => {
  let config: PolicyConfig = { distractions: [], essentials: [] };
  config = setPackage(config, 'com.example.feed', 'distractions');
  expect([...packagesOf(config.distractions)]).toEqual(['com.example.feed']);
  config = setPackage(config, 'com.example.feed', 'essentials');
  expect(config.distractions).toEqual([]);
  expect([...packagesOf(config.essentials)]).toEqual(['com.example.feed']);
  config = setPackage(config, 'com.example.feed', null);
  expect(config.essentials).toEqual([]);
});

test('other groups and domains are preserved', () => {
  const config: PolicyConfig = { distractions: [{ id: 'video', label: 'Video', domains: ['video.example.com'] }], essentials: [] };
  const next = setPackage(config, 'com.example.feed', 'distractions');
  expect(next.distractions.map(group => group.id)).toEqual(['video', 'com.example.feed']);
});

import type { PolicyConfig, TargetGroup } from './api';

// One group per Android app keeps the list readable and editable.
export const appGroup = (pkg: string): TargetGroup => ({ id: pkg, label: pkg, android_packages: [pkg] });

export const packagesOf = (groups: TargetGroup[]) => new Set(groups.flatMap(group => group.android_packages ?? []));

export type ListName = 'distractions' | 'essentials';

/** Moves a package into one list (removing it from the other) or out of both. */
export function setPackage(config: PolicyConfig, pkg: string, target: ListName | null): PolicyConfig {
  const without = (groups: TargetGroup[]) => groups
    .map(group => ({ ...group, android_packages: (group.android_packages ?? []).filter(value => value !== pkg) }))
    .filter(group => (group.android_packages?.length ?? 0) > 0 || (group.mac_apps?.length ?? 0) > 0 || (group.domains?.length ?? 0) > 0);
  const next: PolicyConfig = { ...config, distractions: without(config.distractions ?? []), essentials: without(config.essentials ?? []) };
  if (target) next[target] = [...next[target], appGroup(pkg)];
  return next;
}

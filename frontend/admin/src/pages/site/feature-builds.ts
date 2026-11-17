import type { FeatureBuild } from "@/types";

export interface FeatureBuildGroup {
  key: string;
  name: string;
  versions: FeatureBuild[];
  latest: FeatureBuild;
}

type ParsedSemVer = {
  major: bigint;
  minor: bigint;
  patch: bigint;
  prerelease: string[] | null;
};

const semverPattern = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;

function parseSemVer(version: string): ParsedSemVer | null {
  const match = semverPattern.exec(version);
  if (!match) return null;
  return {
    major: BigInt(match[1]!),
    minor: BigInt(match[2]!),
    patch: BigInt(match[3]!),
    prerelease: match[4] === undefined ? null : match[4].split("."),
  };
}

function compareNumber(left: bigint, right: bigint): number {
  return left === right ? 0 : left < right ? -1 : 1;
}

function compareIdentifier(left: string, right: string): number {
  const leftNumeric = /^\d+$/.test(left);
  const rightNumeric = /^\d+$/.test(right);
  if (leftNumeric && rightNumeric) return compareNumber(BigInt(left), BigInt(right));
  if (leftNumeric !== rightNumeric) return leftNumeric ? -1 : 1;
  return left === right ? 0 : left < right ? -1 : 1;
}

/** Compares SemVer precedence; build metadata does not affect precedence. */
export function compareSemVer(leftVersion: string, rightVersion: string): number {
  const left = parseSemVer(leftVersion);
  const right = parseSemVer(rightVersion);
  if (!left || !right) return leftVersion.localeCompare(rightVersion, "en", { numeric: true });

  for (const part of ["major", "minor", "patch"] as const) {
    const order = compareNumber(left[part], right[part]);
    if (order !== 0) return order;
  }

  if (left.prerelease === null || right.prerelease === null) {
    if (left.prerelease === right.prerelease) return 0;
    return left.prerelease === null ? 1 : -1;
  }

  const sharedLength = Math.min(left.prerelease.length, right.prerelease.length);
  for (let index = 0; index < sharedLength; index += 1) {
    const order = compareIdentifier(left.prerelease[index]!, right.prerelease[index]!);
    if (order !== 0) return order;
  }
  if (left.prerelease.length === right.prerelease.length) return 0;
  return left.prerelease.length < right.prerelease.length ? -1 : 1;
}

function compareBuildsLatestFirst(left: FeatureBuild, right: FeatureBuild): number {
  const semverOrder = compareSemVer(right.version, left.version);
  if (semverOrder !== 0) return semverOrder;

  const registeredOrder = Date.parse(right.createdAt) - Date.parse(left.createdAt);
  if (Number.isFinite(registeredOrder) && registeredOrder !== 0) return registeredOrder;
  return left.version.localeCompare(right.version, "en");
}

/** Groups immutable builds by their logical feature key, with latest SemVer first. */
export function groupFeatureBuilds(builds: FeatureBuild[]): FeatureBuildGroup[] {
  const byKey = new Map<string, FeatureBuild[]>();
  for (const build of builds) {
    const group = byKey.get(build.key) ?? [];
    group.push(build);
    byKey.set(build.key, group);
  }

  const groups: FeatureBuildGroup[] = [];
  for (const [key, versions] of byKey) {
    versions.sort(compareBuildsLatestFirst);
    const latest = versions[0];
    if (!latest) continue;
    groups.push({ key, name: latest.name, versions, latest });
  }
  return groups.sort((left, right) => left.key.localeCompare(right.key, "en"));
}

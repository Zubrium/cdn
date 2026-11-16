import { describe, expect, it } from "vitest";
import type { FeatureBuild } from "@/types";
import { compareSemVer, groupFeatureBuilds } from "./feature-builds";

function build(key: string, version: string, createdAt = "2026-01-01T00:00:00.000Z"): FeatureBuild {
  return {
    id: `${key}@${version}`,
    featureId: key,
    key,
    name: key,
    version,
    schemaVersionId: `schema-${version}`,
    digest: `digest-${version}`,
    artifactSize: 100,
    manifest: {
      formatVersion: 2,
      key,
      name: key,
      version,
      engineApi: "1",
      runtime: { abi: "static-module-v1", entry: "entry.js" },
      slots: ["main"],
      arguments: { type: "object" },
      dataInputs: [],
    },
    createdAt,
  };
}

describe("feature build groups", () => {
  it("sorts SemVer numerically and puts a stable release above prereleases", () => {
    expect(compareSemVer("1.10.0", "1.9.0")).toBeGreaterThan(0);
    expect(compareSemVer("2.0.0", "2.0.0-rc.1")).toBeGreaterThan(0);
    expect(compareSemVer("2.0.0-beta.10", "2.0.0-beta.2")).toBeGreaterThan(0);
  });

  it("shows one logical feature group with latest SemVer first", () => {
    const groups = groupFeatureBuilds([
      build("catalog", "1.9.0"),
      build("header", "1.0.0"),
      build("catalog", "1.10.0"),
      build("catalog", "2.0.0-rc.1"),
      build("catalog", "2.0.0"),
    ]);

    expect(groups.map((group) => group.key)).toEqual(["catalog", "header"]);
    expect(groups[0]?.versions.map((item) => item.version)).toEqual([
      "2.0.0",
      "2.0.0-rc.1",
      "1.10.0",
      "1.9.0",
    ]);
    expect(groups[0]?.latest.version).toBe("2.0.0");
  });

  it("uses the most recently registered build when SemVer precedence ties", () => {
    const groups = groupFeatureBuilds([
      build("catalog", "1.0.0+older", "2026-01-01T00:00:00.000Z"),
      build("catalog", "1.0.0+newer", "2026-01-02T00:00:00.000Z"),
    ]);

    expect(groups[0]?.latest.version).toBe("1.0.0+newer");
  });
});

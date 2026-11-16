import assert from "node:assert/strict";
import { mkdtemp, mkdir, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";

import { resolveAllowedRealPath, resolveFeatureFile } from "./filesystem-policy.mjs";

test("resolveAllowedRealPath rejects symlinks escaping every allowed root", async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), "feature-lab-policy-"));
  try {
    const allowed = path.join(root, "allowed");
    const outside = path.join(root, "outside");
    await mkdir(allowed);
    await mkdir(outside);
    await writeFile(path.join(outside, "secret.txt"), "synthetic-secret");
    await symlink(path.join(outside, "secret.txt"), path.join(allowed, "link.txt"));

    await assert.rejects(
      resolveAllowedRealPath(path.join(allowed, "link.txt"), [allowed]),
      /outside the allowed roots/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("resolveFeatureFile rejects a legacy index symlink outside its feature", async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), "feature-lab-entry-"));
  try {
    const feature = path.join(root, "feature");
    await mkdir(feature);
    await writeFile(path.join(root, "outside.tsx"), "export default function Outside() {}");
    await symlink(path.join(root, "outside.tsx"), path.join(feature, "index.tsx"));

    await assert.rejects(
      resolveFeatureFile(feature, "index.tsx", "legacy source entry"),
      /must not resolve through a symlink outside the feature directory/,
    );
    assert.equal(
      await realpath(path.join(feature, "index.tsx")),
      await realpath(path.join(root, "outside.tsx")),
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

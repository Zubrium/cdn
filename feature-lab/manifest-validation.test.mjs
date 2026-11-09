import assert from "node:assert/strict";
import test from "node:test";
import { validateDataInputManifest } from "./manifest-validation.mjs";

const manifest = (dataInputs) => ({ formatVersion: 2, dataInputs });

test("accepts nested collection fields, enum values and the Engine media profile contract", () => {
  assert.doesNotThrow(() => validateDataInputManifest(manifest([
    {
      key: "products",
      kind: "collection",
      fields: [
        { path: "/products/*/title", type: "text" },
        { path: "/products/*/category", type: "enum", values: ["food", "drink"] },
        { path: "/products/*/images/*/src", type: "media", profile: { accept: "raster-image", renditions: [{ name: "card", width: 600, height: 400, fit: "cover" }] } },
      ],
    },
  ])));
});

test("rejects removed CMS schema keys and missing format 2 inputs", () => {
  assert.throws(() => validateDataInputManifest({ formatVersion: 2, content: { schema: {} }, dataInputs: [] }), /unsupported property/);
  assert.throws(() => validateDataInputManifest({ formatVersion: 2 }), /dataInputs must be an array/);
});

test("rejects duplicate or overlapping paths and invalid wildcards", () => {
  assert.throws(() => validateDataInputManifest(manifest([{ key: "record", kind: "record", fields: [
    { path: "/author", type: "text" }, { path: "/author/name", type: "text" },
  ] }])), /overlapping field path/);
  assert.throws(() => validateDataInputManifest(manifest([{ key: "record", kind: "record", fields: [
    { path: "/*/name", type: "text" },
  ] }])), /invalid array wildcard/);
});

test("rejects media profile aliases and malformed enum contracts", () => {
  assert.throws(() => validateDataInputManifest(manifest([{ key: "image", kind: "scalar", type: "media", media: { accept: "image" } }])), /unsupported property/);
  assert.throws(() => validateDataInputManifest(manifest([{ key: "kind", kind: "scalar", type: "enum", values: ["same", "same"] }])), /unique, non-empty enum values/);
});

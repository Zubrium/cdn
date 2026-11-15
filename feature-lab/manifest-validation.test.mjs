import assert from "node:assert/strict";
import test from "node:test";

import { validateDataInputManifest } from "./manifest-validation.mjs";

function manifestWithRenditions(count) {
  return {
    formatVersion: 2,
    dataInputs: [{
      key: "image",
      kind: "scalar",
      type: "media",
      profile: {
        accept: "raster-image",
        renditions: Array.from({ length: count }, (_, index) => ({
          name: `size_${index}`,
          width: 100 + index,
          height: 100,
          fit: "cover",
        })),
      },
    }],
  };
}

test("manifest validation rejects excessive raster rendition fan-out", () => {
  assert.doesNotThrow(() => validateDataInputManifest(manifestWithRenditions(32)));
  assert.throws(
    () => validateDataInputManifest(manifestWithRenditions(33)),
    /at most 32/,
  );
});

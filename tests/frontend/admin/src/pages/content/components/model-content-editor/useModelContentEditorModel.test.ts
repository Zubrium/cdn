import { describe, expect, it } from "vitest";
import type { ModelContentWorkspace } from "@/types";
import { contentMediaRequirements } from "../../../../../../../../frontend/admin/src/pages/content/components/model-content-editor/useModelContentEditorModel";

describe("contentMediaRequirements", () => {
  it("prepares each pinned placement that uses a shared media field", () => {
    const workspace = {
      snapshot: { id: "snapshot-1" },
      placements: [
        { id: "first", inputs: [{ inputKey: "products", kind: "record", fields: [{ inputPath: "/image", fieldId: "field-1", type: "media", mediaAccept: "raster-image" }] }] },
        { id: "second", inputs: [{ inputKey: "cover", kind: "scalar", fields: [{ inputPath: "value", fieldId: "field-1", type: "media", mediaAccept: "video" }] }] },
      ],
    } as unknown as ModelContentWorkspace;
    expect(contentMediaRequirements(workspace)).toEqual({
      preparations: { "field-1": [
        { snapshotId: "snapshot-1", placementId: "first", schemaKind: "dataInputs", fieldPath: "/products/image" },
        { snapshotId: "snapshot-1", placementId: "second", schemaKind: "dataInputs", fieldPath: "/cover" },
      ] },
      accepts: { "field-1": "none" },
    });
  });
});

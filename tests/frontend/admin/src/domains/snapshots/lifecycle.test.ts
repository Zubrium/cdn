import { describe, expect, it } from "vitest";
import { readySnapshots } from "../../../../../../frontend/admin/src/domains/snapshots/lifecycle";

describe("snapshot lifecycle", () => {
  it("exposes only ready snapshots to editing and publication workflows", () => {
    const snapshots = [
      { id: "ready", state: "ready" },
      { id: "provisioning", state: "provisioning" },
      { id: "failed", state: "failed" },
    ];

    expect(readySnapshots(snapshots)).toEqual([{ id: "ready", state: "ready" }]);
  });

  it("returns an empty list when every snapshot is still being prepared or has failed", () => {
    expect(readySnapshots([
      { id: "provisioning", state: "provisioning" },
      { id: "failed", state: "failed" },
    ])).toEqual([]);
  });
});

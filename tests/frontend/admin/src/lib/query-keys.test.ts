import { describe, expect, it } from "vitest";
import { queryKeys } from "../../../../../frontend/admin/src/lib/query-keys";

describe("token query identity", () => {
  it("isolates creator-scoped token data by authenticated principal", () => {
    expect(queryKeys.tokens.list("moderator-a")).not.toEqual(
      queryKeys.tokens.list("moderator-b"),
    );
  });
});

import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MediaStatusBadge } from "../../../../../../frontend/admin/src/components/common/status-badges";

describe("MediaStatusBadge", () => {
  it("maps every media status to a semantic badge variant", () => {
    const ready = renderToStaticMarkup(createElement(MediaStatusBadge, { status: "ready" }));
    const failed = renderToStaticMarkup(createElement(MediaStatusBadge, { status: "failed" }));
    const deleted = renderToStaticMarkup(createElement(MediaStatusBadge, { status: "deleted" }));

    expect(ready).toContain("bg-success/12");
    expect(failed).toContain("bg-destructive/12");
    expect(deleted).toContain("bg-muted");
  });
});

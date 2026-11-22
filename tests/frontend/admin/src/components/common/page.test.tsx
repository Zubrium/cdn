import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ContextHelp } from "../../../../../../frontend/admin/src/components/common/page";
import { TooltipProvider } from "../../../../../../frontend/admin/src/components/ui/tooltip";

describe("ContextHelp", () => {
  it("provides a 44px keyboard and touch target", () => {
    const markup = renderToStaticMarkup(
      createElement(TooltipProvider, null, createElement(ContextHelp, { label: "Help" }, "Helpful details")),
    );

    expect(markup).toContain("h-11 w-11");
  });

  it("associates the help text with its trigger for assistive technology", () => {
    const markup = renderToStaticMarkup(
      createElement(TooltipProvider, null, createElement(ContextHelp, { label: "Help" }, "Helpful details")),
    );

    expect(markup).toContain('aria-describedby="');
    expect(markup).not.toContain("aria-description");
  });
});

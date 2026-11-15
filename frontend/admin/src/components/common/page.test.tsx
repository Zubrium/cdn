import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ContextHelp } from "./page";

describe("ContextHelp", () => {
  it("provides a 44px keyboard and touch target", () => {
    const markup = renderToStaticMarkup(
      createElement(ContextHelp, { label: "Help" }, "Helpful details"),
    );

    expect(markup).toContain("h-11 w-11");
  });
});

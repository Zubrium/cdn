import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import {
  ContextHelp,
  InlineAlert,
  TableSkeleton,
} from "../../../../../../frontend/admin/src/components/common/page";
import { TooltipProvider } from "../../../../../../frontend/admin/src/components/ui/tooltip";

function renderHelp() {
  return renderToStaticMarkup(
    createElement(TooltipProvider, null, createElement(ContextHelp, { label: "Help" }, "Helpful details")),
  );
}

describe("ContextHelp", () => {
  it("provides a 44px keyboard and touch target", () => {
    expect(renderHelp()).toContain("h-11 w-11");
  });

  it("associates the help text with its trigger for assistive technology", () => {
    const markup = renderHelp();

    expect(markup).toContain('aria-describedby="');
    expect(markup).not.toContain("aria-description");
  });

  it("does not claim an expandable tooltip state", () => {
    expect(renderHelp()).not.toContain("aria-expanded");
  });
});

describe("InlineAlert", () => {
  it("announces warnings and danger immediately", () => {
    for (const tone of ["warning", "danger"] as const) {
      const markup = renderToStaticMarkup(createElement(InlineAlert, { tone }, "Сообщение"));

      expect(markup).toContain('role="alert"');
    }
  });

  it("uses a passive status for informational and success messages", () => {
    for (const tone of ["info", "success"] as const) {
      const markup = renderToStaticMarkup(createElement(InlineAlert, { tone }, "Сообщение"));

      expect(markup).toContain('role="status"');
      expect(markup).not.toContain('role="alert"');
    }
  });
});

describe("TableSkeleton", () => {
  it("announces table loading instead of hiding it from assistive technology", () => {
    const markup = renderToStaticMarkup(createElement(TableSkeleton, { rows: 2 }));

    expect(markup).toContain('role="status"');
    expect(markup).toContain('aria-label="Загрузка"');
    expect(markup).not.toContain('aria-hidden="true"');
  });
});

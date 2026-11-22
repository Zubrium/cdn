import { createElement, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

vi.mock("@radix-ui/react-alert-dialog", async () => {
  const { createElement: h } = await import("react");
  const passthrough = ({ children }: { children?: ReactNode }) => children;
  const element = (tag: string) => (props: Record<string, unknown>) => h(tag, props);
  return {
    Root: passthrough,
    Trigger: passthrough,
    Portal: passthrough,
    Overlay: element("div"),
    Content: element("div"),
    Title: element("h2"),
    Description: element("p"),
    Action: element("button"),
    Cancel: element("button"),
  };
});

vi.mock("@radix-ui/react-dialog", async () => {
  const { createElement: h } = await import("react");
  const passthrough = ({ children }: { children?: ReactNode }) => children;
  const element = (tag: string) => (props: Record<string, unknown>) => h(tag, props);
  return {
    Root: passthrough,
    Trigger: passthrough,
    Close: element("button"),
    Portal: passthrough,
    Overlay: element("div"),
    Content: element("div"),
  };
});

import {
  AlertDialog,
  AlertDialogContent,
} from "../../../../../frontend/admin/src/components/ui/alert-dialog";
import {
  Sheet,
  SheetContent,
} from "../../../../../frontend/admin/src/components/ui/sheet";

describe("AlertDialogContent", () => {
  it("limits height and scrolls so the footer stays reachable on small screens", () => {
    const markup = renderToStaticMarkup(
      createElement(AlertDialog, null, createElement(AlertDialogContent, null, "Подтверждение")),
    );

    expect(markup).toContain("max-h-[calc(100dvh-2rem)]");
    expect(markup).toContain("overflow-y-auto");
    expect(markup).toContain("overscroll-contain");
    expect(markup).toContain("thin-scrollbar");
    expect(markup).toContain("-translate-x-1/2");
    expect(markup).toContain("-translate-y-1/2");
  });
});

describe("SheetContent", () => {
  it("exposes a labelled 44px close control by default", () => {
    const markup = renderToStaticMarkup(
      createElement(Sheet, null, createElement(SheetContent, null, "Меню")),
    );

    expect(markup).toContain('aria-label="Закрыть"');
    expect(markup).toContain("size-11");
    expect(markup).toContain("items-center");
    expect(markup).toContain("justify-center");
    expect(markup).toContain("p-0");
  });
});

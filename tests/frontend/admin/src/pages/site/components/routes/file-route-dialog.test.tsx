import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/domains/media/MediaPickerDialog", () => ({
  MediaPickerDialog: () => null,
}));
vi.mock("@/components/ui/dialog", () => ({
  Dialog: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DialogContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogDescription: ({ children }: { children: React.ReactNode }) => <p>{children}</p>,
  DialogFooter: ({ children }: { children: React.ReactNode }) => <footer>{children}</footer>,
  DialogHeader: ({ children }: { children: React.ReactNode }) => <header>{children}</header>,
  DialogTitle: ({ children }: { children: React.ReactNode }) => <h1>{children}</h1>,
}));

import { FileRouteDialog } from "../../../../../../../../frontend/admin/src/pages/site/components/routes/FileRouteDialog";
import { parseStaticFilePath } from "../../../../../../../../frontend/admin/src/pages/site/components/routes/route-utils";

describe("file route dialog", () => {
  it("shows fixed-path and public-file choices without a delivery-mode control", () => {
    const markup = renderToStaticMarkup(
      <FileRouteDialog
        open
        existingPaths={["/existing.pdf"]}
        onClose={() => {}}
        onCreate={() => {}}
      />,
    );

    expect(markup).toContain("Новый файловый маршрут");
    expect(markup).toContain('value="/file"');
    expect(markup).toContain("Только фиксированный путь");
    expect(markup).toContain("Выбрать публичный файл");
    expect(markup).not.toContain("Поведение");
    expect(markup).not.toContain("Открывать, если браузер поддерживает формат");
    expect(markup).not.toContain("Скачивать файлом");
    expect(markup).toContain("Создать маршрут");
  });

  it("accepts fixed file paths and rejects dynamic route constructs", () => {
    expect(parseStaticFilePath(" /documents/price-list.pdf ")).toEqual({
      path: "/documents/price-list.pdf",
    });
    expect(parseStaticFilePath("/")).toEqual({ path: "/" });

    for (const value of ["/documents/:year", "/documents/*path", "/documents/~(?<slug>.+)"]) {
      expect(parseStaticFilePath(value)).toEqual({
        error: "Файловый маршрут должен быть фиксированным: параметры, regex и catch-all не поддерживаются.",
      });
    }
  });
});

import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { SitepackImportAction, SitepackImportFailureDetails, SitepackImportItems } from "./SitepackActions";

describe("sitepack import feedback", () => {
  it("opens the native archive picker through an associated label", () => {
    const queryClient = new QueryClient();
    const html = renderToStaticMarkup(createElement(QueryClientProvider, { client: queryClient }, createElement(SitepackImportAction)));
    const inputId = html.match(/<input[^>]*id="([^"]+)"[^>]*type="file"/)?.[1];

    expect(inputId).toBeTruthy();
    expect(html).toContain(`<label for="${inputId}"`);
    expect(html).not.toContain("onclick=");
    queryClient.clear();
  });

  it("shows the safe rejected item locator and controlled reason", () => {
    const html = renderToStaticMarkup(createElement(SitepackImportFailureDetails, {
      message: "Импорт отклонён целиком; новый снапшот не создан.",
      details: [{ path: "files[2].renditions[0]", code: "invalid_rendition_file", message: "Содержимое варианта файла не прошло проверку" }],
    }));

    expect(html).toContain("files[2].renditions[0]");
    expect(html).toContain("Содержимое варианта файла не прошло проверку");
    expect(html).not.toContain("secret-token");
  });

  it("lists bounded model remaps without exposing record values", () => {
    const items = [
        { kind: "file", path: "files[0]", action: "imported", sourceId: "file-source", targetId: "file-target", message: "Файл перенесён; медиа-ссылки переназначены" },
        { kind: "model_entity", path: "dataModel.entities", action: "ids_remapped", count: 2, message: "ID сущностей модели переназначены" },
        { kind: "model_record", path: "modelContent.records", action: "ids_remapped", count: 4, message: "ID записей модели переназначены" },
      ] as const;
    const html = renderToStaticMarkup(createElement(SitepackImportItems, { items: [...items], truncated: true }));

    expect(html).toContain("file-source → file-target");
    expect(html).toContain("dataModel.entities · ссылок: 2");
    expect(html).toContain("modelContent.records · ссылок: 4");
    expect(html).toContain("Показаны первые 3 элементов");
    expect(html).not.toContain("private content value");
  });
});

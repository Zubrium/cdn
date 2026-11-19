import { useSyncExternalStore } from "react";
import { getSyntheticDataMode, subscribeSyntheticDataMode } from "@/api";
import { PageHeader, useDocumentTitle } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";

const previewBranches = [
  { name: "основная", address: "127.0.0.1:8081", indexing: "Разрешена", origins: "https://example.invalid" },
  { name: "предпросмотр", address: "127.0.0.1:8082", indexing: "Закрыта", origins: "Не заданы" },
];

export default function SettingsPage() {
  useDocumentTitle("Настройки");
  const synthetic = useSyncExternalStore(
    subscribeSyntheticDataMode,
    getSyntheticDataMode,
    () => false,
  );

  return (
    <div className="page-grid">
      <PageHeader
        title="Конфигурация запуска"
        description="Состав веток, адреса, CORS и индексация задаются в YAML при запуске."
      />
      {synthetic ? (
        <section className="overflow-hidden rounded-lg border border-warning/40 bg-card" aria-labelledby="runtime-preview-title">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
            <div>
              <h2 id="runtime-preview-title" className="font-semibold">Пример runtime-конфигурации</h2>
              <p className="mt-1 text-sm text-muted-foreground">Макет — конфигурация не загружена; значения не сохраняются.</p>
            </div>
            <Badge variant="warning">Только просмотр</Badge>
          </div>
          <div className="divide-y divide-border">
            {previewBranches.map((branch) => (
              <dl key={branch.name} className="grid gap-x-6 gap-y-3 px-4 py-4 text-sm sm:grid-cols-2">
                <div><dt className="text-muted-foreground">Ветка</dt><dd className="mt-1 font-medium">{branch.name}</dd></div>
                <div><dt className="text-muted-foreground">Адрес прослушивания</dt><dd className="mt-1 font-mono text-xs">{branch.address}</dd></div>
                <div><dt className="text-muted-foreground">Индексация</dt><dd className="mt-1">{branch.indexing}</dd></div>
                <div><dt className="text-muted-foreground">CORS origins</dt><dd className="mt-1 break-all">{branch.origins}</dd></div>
              </dl>
            ))}
          </div>
        </section>
      ) : (
        <section className="rounded-lg border border-border bg-card p-4" aria-labelledby="runtime-config-title">
          <h2 id="runtime-config-title" className="font-semibold">Обзор недоступен через Admin API</h2>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-muted-foreground">
            Текущий Presentation contract не предоставляет endpoint для чтения runtime-конфигурации. Изменения и проверку веток выполняйте в startup YAML; интерфейс не запрашивает удалённый Website branches API.
          </p>
        </section>
      )}
    </div>
  );
}

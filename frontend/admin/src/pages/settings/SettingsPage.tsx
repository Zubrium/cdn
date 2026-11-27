import { PageHeader, useDocumentTitle } from "@/components/common/page";

export default function SettingsPage() {
  useDocumentTitle("Настройки");

  return (
    <div className="page-grid">
      <PageHeader
        title="Конфигурация запуска"
        description="Один публичный listener; адрес и параметры задаются в startup YAML."
      />
      <section className="rounded-lg border border-border bg-card p-4" aria-labelledby="runtime-config-title">
        <h2 id="runtime-config-title" className="font-semibold">Обзор недоступен через Admin API</h2>
        <p className="mt-2 max-w-2xl text-sm leading-6 text-muted-foreground">
          Продукт использует один публичный listener. Адрес прослушивания и параметры определяются в startup YAML; текущий Presentation contract не предоставляет endpoint для чтения runtime-конфигурации. Интерфейс не запрашивает удалённый конфигурационный API и не показывает примерные значения как реальные.
        </p>
      </section>
    </div>
  );
}

import { PageHeader, useDocumentTitle } from "@/components/common/page";

export default function DesignSystemPage() {
  useDocumentTitle("Дизайн-токены");
  return (
    <div className="page-grid">
      <PageHeader title="Дизайн-токены" description="Токены и значения объявляются манифестами фич." />
      <section className="rounded-xl border border-border bg-card p-4" aria-labelledby="feature-tokens-title">
        <h2 id="feature-tokens-title" className="font-semibold">Редактор дизайн-системы отключён</h2>
        <p className="mt-2 text-sm text-muted-foreground">
          Website snapshot не хранит базовые design tokens. Настройте токены в manifest фичи; этот экран не обращается к неподдерживаемому Website API.
        </p>
      </section>
    </div>
  );
}

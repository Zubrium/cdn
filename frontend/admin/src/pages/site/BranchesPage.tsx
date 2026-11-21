import { useSyncExternalStore } from "react";
import { getSyntheticDataMode, subscribeSyntheticDataMode } from "@/api";
import { PageHeader, useDocumentTitle } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";

const previewAssignments = [
  { branch: "основная", snapshot: "#18 · Каталог" },
  { branch: "предпросмотр", snapshot: "#21 · Новая главная" },
];

export default function BranchesPage() {
  useDocumentTitle("Назначения снапшотов");
  const synthetic = useSyncExternalStore(
    subscribeSyntheticDataMode,
    getSyntheticDataMode,
    () => false,
  );

  return (
    <div className="page-grid">
      <PageHeader
        title="Назначения снапшотов"
        description="Каждая ветка независимо указывает на снапшот. Состав веток и их runtime-параметры задаются в startup YAML."
      />
      {synthetic ? (
        <section className="overflow-hidden rounded-lg border border-warning/40 bg-card" aria-labelledby="branch-preview-title">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
            <div>
              <h2 id="branch-preview-title" className="font-semibold">Пример назначений</h2>
              <p className="mt-1 text-sm text-muted-foreground">Макет — данные не загружены и не сохраняются.</p>
            </div>
            <Badge variant="warning">Только просмотр</Badge>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[32rem] text-left text-sm">
              <thead className="bg-muted/40 text-xs text-muted-foreground">
                <tr>
                  <th scope="col" className="px-4 py-2.5 font-medium">Ветка</th>
                  <th scope="col" className="px-4 py-2.5 font-medium">Назначенный снапшот</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {previewAssignments.map((item) => (
                  <tr key={item.branch}>
                    <th scope="row" className="px-4 py-3 font-medium">{item.branch}</th>
                    <td className="px-4 py-3">{item.snapshot}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : (
        <section className="rounded-lg border border-border bg-card p-4">
          <h2 className="font-semibold">Назначения недоступны через Admin API</h2>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-muted-foreground">
            В текущем Presentation contract нет endpoint для чтения или изменения назначений. Интерфейс не отправляет запросы к Website branches API и не показывает примерные данные как реальные.
          </p>
        </section>
      )}
    </div>
  );
}

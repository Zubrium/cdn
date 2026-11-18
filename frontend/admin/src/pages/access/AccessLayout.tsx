import { Outlet } from "react-router-dom";
import { PageHeader, useDocumentTitle } from "@/components/common/page";

export default function AccessLayout() {
  useDocumentTitle("Доступы · Учётные записи");
  return (
    <div className="page-grid">
      <PageHeader
        title="Доступы"
        description="Одна учётная запись для человека или интеграции. Назначайте права отдельно для каждой группы."
      />
      <Outlet />
    </div>
  );
}

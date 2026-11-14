import { PageHeader, useDocumentTitle } from "@/components/common/page";
import { ExternalBackendSettings } from "./components/workspace/ExternalBackendSettings";
import { useSiteWorkspace } from "./SiteWorkspace";

export default function BackendPage() {
  useDocumentTitle("Бэкенд");
  const { config, update } = useSiteWorkspace();
  return <div className="page-grid min-w-0">
    <PageHeader
      title="Бэкенд"
      description="Публичные адреса API и общие значения, которые фичи могут использовать в запросах. Секреты здесь не хранятся."
    />
    <ExternalBackendSettings
      config={config}
      onBackendsChange={(backends) => update((current) => ({ ...current, backends }))}
      onConstantsChange={(publicConstants) => update((current) => ({ ...current, publicConstants }))}
    />
  </div>;
}

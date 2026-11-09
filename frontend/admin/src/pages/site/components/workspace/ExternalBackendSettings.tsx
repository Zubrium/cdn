import type { PublicBackend, PublicSnapshotConstant, SiteConfig } from "@/types";
import { BackendRegistryEditor } from "./BackendRegistryEditor";
import { PublicConstantsEditor } from "./PublicConstantsEditor";

export function ExternalBackendSettings({ config, onBackendsChange, onConstantsChange }: {
  config: SiteConfig;
  onBackendsChange: (next: PublicBackend[]) => void;
  onConstantsChange: (next: PublicSnapshotConstant[]) => void;
}) {
  return <section className="page-grid min-w-0 border-t border-border pt-6">
    <div className="space-y-1">
      <h2 className="text-lg font-semibold">Публичные API сайта</h2>
      <p className="text-sm text-muted-foreground">Адреса и значения хранятся в выбранном снапшоте. Фичи обращаются к ним напрямую из браузера; API должен разрешать origin сайта через CORS.</p>
    </div>
    <BackendRegistryEditor config={config} backends={config.backends ?? []} onChange={onBackendsChange} />
    <PublicConstantsEditor config={config} constants={config.publicConstants ?? []} onChange={onConstantsChange} />
  </section>;
}

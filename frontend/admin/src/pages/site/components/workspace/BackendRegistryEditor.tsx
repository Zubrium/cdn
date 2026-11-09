import type { PublicBackend, SiteConfig } from "@/types";
import { useConfirm } from "@/components/common/confirm";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

function backendUsers(config: SiteConfig, backendId: string): string[] {
  const uses: string[] = [];
  for (const page of config.pages) {
    for (const placement of page.placements) {
      if (Object.values(placement.backendRequests ?? {}).some((request) => request.backendId === backendId)) {
        uses.push(`${page.name} · ${placement.build.featureKey}`);
      }
    }
  }
  return [...new Set(uses)];
}

export function BackendRegistryEditor({ config, backends, onChange }: {
  config: SiteConfig;
  backends: PublicBackend[];
  onChange: (next: PublicBackend[]) => void;
}) {
  const confirm = useConfirm();
  const addBackend = () => onChange([...backends, { id: crypto.randomUUID(), name: "", baseUrl: "" }]);

  return <div className="grid min-w-0 gap-3">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <h3 className="text-sm font-medium">Backend-адреса</h3>
      <Button type="button" variant="outline" className="min-h-11" onClick={addBackend}>Добавить backend</Button>
    </div>
    {backends.length === 0 && <p className="rounded-lg border border-dashed border-border p-4 text-sm text-muted-foreground">Зарегистрированных адресов пока нет. Запросы текущего сайта можно настраивать без регистрации.</p>}
    {backends.map((backend) => {
      const users = backendUsers(config, backend.id);
      const updateBackend = (patch: Partial<PublicBackend>) => onChange(backends.map((item) => item.id === backend.id ? { ...item, ...patch } : item));
      return <fieldset key={backend.id} className="grid min-w-0 gap-3 rounded-lg border border-border bg-card p-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto]">
        <legend className="sr-only">Публичный backend</legend>
        <label className="space-y-1.5 text-sm">
          <span>Название</span>
          <Input className="min-h-11" value={backend.name} onChange={(event) => updateBackend({ name: event.currentTarget.value })} placeholder="Каталог API" />
        </label>
        <label className="space-y-1.5 text-sm">
          <span>Публичный origin</span>
          <Input className="min-h-11" type="url" value={backend.baseUrl} disabled={users.length > 0} onChange={(event) => updateBackend({ baseUrl: event.currentTarget.value })} placeholder="https://api.example.com" />
          <span className="block text-xs text-muted-foreground">Только origin, без пути, query, fragment и учётных данных. Production требует HTTPS.</span>
        </label>
        <Button type="button" variant="outline" className="min-h-11 self-end text-destructive" aria-label={`Удалить backend ${backend.name || "без названия"}`} disabled={users.length > 0} onClick={async () => {
          if (!(await confirm({ title: "Удалить backend?", description: `Адрес «${backend.name || backend.baseUrl || "без названия"}» будет удалён из рабочей копии сайта.`, confirmLabel: "Удалить", destructive: true }))) return;
          onChange(backends.filter((item) => item.id !== backend.id));
        }}>Удалить</Button>
        {users.length > 0 && <p className="text-xs text-warning sm:col-span-3">Сначала перепривяжите размещения: {users.join(", ")}. Адрес используемого backend нельзя незаметно заменить.</p>}
      </fieldset>;
    })}
  </div>;
}

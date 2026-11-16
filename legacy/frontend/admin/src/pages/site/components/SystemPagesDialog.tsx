import { useMemo } from "react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import type { SiteConfig, SitePage } from "@/types";
import { staticPageRouteIds } from "../route-tree";

interface SystemPagesDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  config: SiteConfig;
  update: (next: (current: SiteConfig) => SiteConfig) => void;
}

export function systemPageCandidates(pages: SitePage[], staticPageIds: ReadonlySet<string>): SitePage[] {
  return pages.filter((page) => staticPageIds.has(page.id));
}

export function setSystemPageAssignment(config: SiteConfig, status: string, pageId: string): SiteConfig {
  const systemPages = { ...config.systemPages };
  if (pageId) systemPages[status] = pageId;
  else delete systemPages[status];
  return { ...config, systemPages };
}

export function SystemPagesDialog({ open, onOpenChange, config, update }: SystemPagesDialogProps) {
  const staticPageIds = useMemo(() => staticPageRouteIds(config.routeTree), [config.routeTree]);
  const availablePages = systemPageCandidates(config.pages, staticPageIds);
  return <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader><DialogTitle>Системные страницы</DialogTitle><DialogDescription>Выберите страницу со статическим маршрутом. Изменения сохраняются вместе со структурой в новом снапшоте.</DialogDescription></DialogHeader>
        <div className="space-y-4">
          {([ ["404", "Страница не найдена"], ["500", "Ошибка сайта"] ] as const).map(([status, label]) => {
            const selected = config.systemPages?.[status] ?? "";
            const missingRoute = Boolean(selected && !staticPageIds.has(selected));
            return <div key={status} className="space-y-1.5">
              <Label htmlFor={`system-page-${status}`}>{label} · {status}</Label>
              <select id={`system-page-${status}`} className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm" value={selected} onChange={(event) => {
                const value = event.currentTarget.value;
                update((current) => setSystemPageAssignment(current, status, value));
              }}>
                <option value="">Встроенная страница</option>
                {missingRoute && <option value={selected}>{config.pages.find((page) => page.id === selected)?.name ?? "Страница недоступна"} · нет статического маршрута</option>}
                {availablePages.map((page) => <option key={page.id} value={page.id}>{page.name}</option>)}
              </select>
              {missingRoute && <p role="alert" className="text-xs text-warning">Добавьте статический маршрут или выберите другую страницу. Пока используется встроенный вариант.</p>}
            </div>;
          })}
          <p className="text-xs text-muted-foreground">Для ветки без назначенного снапшота показывается встроенная страница «Сайт ещё не настроен». Отдельная страница 403 пока не вызывается runtime.</p>
          {(["403", "503"] as const).filter((status) => config.systemPages?.[status]).map((status) => <div key={status} className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-3 text-sm"><span>Сохранено назначение {status}: {config.pages.find((page) => page.id === config.systemPages?.[status])?.name ?? "страница недоступна"}. Сейчас используется встроенная страница.</span><Button type="button" variant="outline" size="sm" onClick={() => update((current) => setSystemPageAssignment(current, status, ""))}>Снять назначение</Button></div>)}
        </div>
        <DialogFooter><Button type="button" onClick={() => onOpenChange(false)}>Готово</Button></DialogFooter>
      </DialogContent>
    </Dialog>;
}

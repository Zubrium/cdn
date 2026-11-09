import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import type { RouteTreeNode, SitePage } from "@/types";
import { describeRoutes } from "../../route-tree";
import type { RouteDialogState } from "./route-types";
import { childNodeFromSegment, comparableRootPath, currentNode, defaultRootPath, isDescendant, pageName, parseAbsolutePath, readRootPath, readRouteSegment, suggestedName } from "./route-utils";

export function RouteEditorDialog({
  state,
  pages,
  tree,
  onClose,
  onApply,
}: {
  state: RouteDialogState | null;
  pages: SitePage[];
  tree: RouteTreeNode;
  onClose: () => void;
  onApply: (route: RouteTreeNode, parentId?: string | null, page?: SitePage) => void;
}) {
  const node = state?.mode === "edit" ? currentNode(tree, state.routeId) : undefined;
  const descriptor = state?.mode === "edit" ? describeRoutes(tree).find((route) => route.id === state.routeId) : undefined;
  const isRoot = state?.mode === "root" || (state?.mode === "edit" && descriptor?.parentId === undefined);
  const [value, setValue] = useState("");
  const [pageTarget, setPageTarget] = useState("__new");
  const [name, setName] = useState("");
  const [kind, setKind] = useState<SitePage["kind"]>("page");
  const [error, setError] = useState("");

  useEffect(() => {
    if (!state) return;
    const rootPath = defaultRootPath(tree);
    setValue(state.mode === "edit" && node ? (isRoot ? readRootPath(node) : readRouteSegment(node)) : state.mode === "root" ? rootPath : "new");
    setPageTarget(state.mode === "edit" && node?.pageId ? node.pageId : state.existingPageId ?? (state.chooseExisting ? "__select" : "__new"));
    setName(state.mode === "edit" && node?.pageId ? pageName(pages, node.pageId) ?? "Новая страница" : suggestedName(state.mode === "root" ? rootPath : "new"));
    setKind("page");
    setError("");
  }, [state, node, descriptor?.path, isRoot, pages, tree]);

  const submit = () => {
    if (!state) return;
    const id = node?.id ?? `route-${crypto.randomUUID().slice(0, 8)}`;
    const selectedPage = pageTarget !== "__new" && pageTarget !== "__none" ? pages.find((page) => page.id === pageTarget) : undefined;
    const page = pageTarget === "__new" ? { id: `page-${crypto.randomUUID().slice(0, 8)}`, name: name.trim() || suggestedName(value), kind, placements: [] } satisfies SitePage : undefined;
    const pageId = selectedPage?.id ?? page?.id ?? (pageTarget === "__none" ? undefined : node?.pageId);
    let nextNode: RouteTreeNode | undefined;
    let validationError: string | undefined;

    if (isRoot) {
      const parsed = parseAbsolutePath(value);
      validationError = parsed.error;
      if (parsed.path) nextNode = { id, type: "path", path: parsed.path, ...(pageId ? { pageId } : {}), children: node?.children ?? [] };
    } else {
      const parent = state.parentId ? currentNode(tree, state.parentId) : undefined;
      if (parent?.type === "catchAll" || (parent?.type === "path" && parent.path?.split("/").filter(Boolean).at(-1)?.startsWith("*"))) {
        setError("Catch-all маршрут не может содержать дочерние узлы.");
        return;
      }
      const parsed = childNodeFromSegment(value, id, pageId, node?.children ?? []);
      nextNode = parsed.node;
      validationError = parsed.error;
    }
    if (!validationError && !nextNode) validationError = "Не удалось собрать маршрут.";
    if (!validationError && pageTarget === "__none" && !(node?.children?.length)) validationError = "Листовой маршрут должен быть связан со страницей.";
    if (validationError) { setError(validationError); return; }
    if (!nextNode) { setError("Не удалось собрать маршрут."); return; }

    if (state.mode !== "edit" && !pageId) { setError("Выберите существующую страницу или создайте новую."); return; }
    if (state.mode === "edit" && pageTarget === "__new" && !node?.pageId && !page) { setError("Укажите название новой страницы."); return; }
    const current = describeRoutes(tree).filter((route) => route.id !== node?.id);
    const candidate = nextNode;
    if (isRoot && candidate.type === "path") {
      const equivalent = comparableRootPath(candidate.path ?? "/");
      if (current.some((route) => route.path === equivalent)) { setError("Такой URL уже настроен. Измените путь или привяжите нужную страницу к существующему узлу."); return; }
    }
    const parentId = state.mode === "child" ? state.parentId ?? null : undefined;
    if (state.mode === "edit") onApply(nextNode, undefined, page);
    else onApply(nextNode, parentId ?? null, page);
  };

  return <Dialog open={Boolean(state)} onOpenChange={(open) => { if (!open) onClose(); }}>
    <DialogContent onOpenAutoFocus={(event) => {
      if (!window.matchMedia("(pointer: coarse)").matches) return;
      event.preventDefault();
      const content = event.currentTarget as HTMLElement | null;
      content?.querySelector<HTMLButtonElement>("[data-route-dialog-cancel]")?.focus();
    }}>
      <DialogHeader><DialogTitle>{state?.mode === "edit" ? "Настроить маршрут" : state?.mode === "child" ? "Добавить дочерний маршрут" : "Добавить самостоятельный маршрут"}</DialogTitle><DialogDescription>{isRoot ? "Корень задаёт полный URL. Дочерние узлы продолжат его следующими сегментами." : "Укажите один сегмент, который добавится к пути родителя."}</DialogDescription></DialogHeader>
      <div className="space-y-4">
        <div className="space-y-1.5"><Label htmlFor="route-value">{isRoot ? "Полный URL" : "Сегмент"}</Label><Input className="min-h-11" id="route-value" value={value} onChange={(event) => { setValue(event.currentTarget.value); if (pageTarget === "__new") setName(suggestedName(event.currentTarget.value)); setError(""); }} placeholder={isRoot ? "/about" : "products, :slug, ~(?<id>[0-9]+), *path"} /><p className="text-xs leading-5 text-muted-foreground">Статический текст · :параметр · ~regex-сегмент · *catch-all. Catch-all может быть только последним.</p></div>
        <div className="space-y-1.5"><Label htmlFor="route-page">Страница</Label><select id="route-page" className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={pageTarget} onChange={(event) => { setPageTarget(event.currentTarget.value); setError(""); }}><option value="__new">Создать страницу</option>{pageTarget === "__select" && <option value="__select" disabled>Выберите страницу</option>}{state?.mode === "edit" && Boolean(node?.children?.length) && <option value="__none">Оставить узел без страницы</option>}{pages.map((page) => <option key={page.id} value={page.id}>{page.name}</option>)}</select></div>
        {pageTarget === "__new" && <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_12rem]"><div className="space-y-1.5"><Label htmlFor="route-page-name">Название страницы</Label><Input className="min-h-11" id="route-page-name" value={name} onChange={(event) => setName(event.currentTarget.value)} /></div><label className="space-y-1.5 text-sm"><span className="font-medium">Тип страницы</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={kind} onChange={(event) => setKind(event.currentTarget.value as SitePage["kind"])}><option value="page">Обычная страница</option><option value="collection-list">Список коллекции</option><option value="detail">Карточка записи</option></select></label></div>}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      </div>
      <DialogFooter><Button type="button" variant="outline" className="min-h-11" data-route-dialog-cancel onClick={onClose}>Отмена</Button><Button type="button" className="min-h-11" onClick={submit}>{state?.mode === "edit" ? "Сохранить маршрут" : "Создать"}</Button></DialogFooter>
    </DialogContent>
  </Dialog>;
}

export function RouteParentDialog({ routeId, tree, onClose, onApply }: { routeId: string | null; tree: RouteTreeNode; onClose: () => void; onApply: (routeId: string, parentId: string | null) => void }) {
  const routes = useMemo(() => describeRoutes(tree), [tree]);
  const route = routes.find((item) => item.id === routeId);
  const [parentId, setParentId] = useState("__root");

  useEffect(() => {
    setParentId(route?.parentId ?? "__root");
  }, [routeId, route?.parentId]);

  const choices = routeId ? routes.filter((item) => {
    if (item.id === routeId || isDescendant(tree, routeId, item.id)) return false;
    return item.node.type !== "catchAll" && !(item.node.type === "path" && item.node.path?.split("/").filter(Boolean).at(-1)?.startsWith("*"));
  }) : [];
  const indexRoute = route?.node.type === "index";

  return <Dialog open={Boolean(routeId)} onOpenChange={(open) => { if (!open) onClose(); }}>
    <DialogContent>
      <DialogHeader><DialogTitle>Переместить маршрут</DialogTitle><DialogDescription>{route ? `Выберите родительский маршрут для ${route.path}.` : "Выберите родительский маршрут."} Это действие меняет только структуру маршрутов.</DialogDescription></DialogHeader>
      <div className="space-y-1.5">
        <Label htmlFor="route-parent">Родительский маршрут</Label>
        <select id="route-parent" className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" value={indexRoute ? "__root" : parentId} onChange={(event) => setParentId(event.currentTarget.value)} disabled={indexRoute}>
          <option value="__root">В корень сайта</option>
          {!indexRoute && choices.map((item) => <option key={item.id} value={item.id}>{item.path}</option>)}
        </select>
        {indexRoute && <p className="text-xs text-muted-foreground">Главный маршрут нельзя вложить в другой маршрут.</p>}
      </div>
      <DialogFooter><Button type="button" variant="outline" className="min-h-11" onClick={onClose}>Отмена</Button><Button type="button" className="min-h-11" disabled={!routeId || indexRoute || parentId === (route?.parentId ?? "__root")} onClick={() => routeId && onApply(routeId, parentId === "__root" ? null : parentId)}>Переместить</Button></DialogFooter>
    </DialogContent>
  </Dialog>;
}

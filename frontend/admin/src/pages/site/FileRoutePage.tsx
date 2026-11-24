import { useEffect, useMemo, useState } from "react";
import { ArrowLeft, FileUp, Save, Trash2 } from "lucide-react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/api";
import { useConfirm } from "@/components/common/confirm";
import { useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { MediaPickerDialog } from "@/domains/media/MediaPickerDialog";
import type { Media } from "@/types";
import { formatBytes } from "@/lib/format";
import { useSiteWorkspace } from "./SiteWorkspace";
import { describeRoutes, removeRouteBranch, updateRouteNode } from "./route-tree";
import { comparableRootPath, parseStaticFilePath } from "./components/routes/route-utils";
import { siteContextQuery } from "./site-query";

export default function FileRoutePage() {
  const { routeId = "" } = useParams();
  const { config, update } = useSiteWorkspace();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const routes = useMemo(() => describeRoutes(config.routeTree), [config.routeTree]);
  const route = routes.find((item) => item.id === routeId && Boolean(item.node.fileId));
  const [path, setPath] = useState(route?.path ?? "");
  const [pickerOpen, setPickerOpen] = useState(false);
  const [pathError, setPathError] = useState("");
  const media = useQuery({
    queryKey: ["media", route?.node.fileId],
    queryFn: ({ signal }) => api.media.get(route!.node.fileId!, signal),
    enabled: Boolean(route?.node.fileId),
  });
  const snapshotId = searchParams.get("snapshot") ?? "";
  const listURL = `/site/pages${siteContextQuery(snapshotId, searchParams.get("page_id") ?? "")}`;
  useDocumentTitle(route?.node.fileId ? media.data?.original_filename ?? "Файловый маршрут" : "Файловый маршрут");

  useEffect(() => {
    setPath(route?.path ?? "");
    setPathError("");
  }, [route?.path]);

  if (!route?.node.fileId) {
    return <div className="flex h-full items-center justify-center p-6">
      <div className="max-w-md text-center">
        <h1 className="text-xl font-semibold">Файловый маршрут не найден</h1>
        <p className="mt-2 text-sm text-muted-foreground">Он мог быть удалён из рабочей копии.</p>
        <Button asChild variant="outline" className="mt-5 min-h-11"><Link to={listURL}><ArrowLeft />К страницам</Link></Button>
      </div>
    </div>;
  }

  const applyPath = () => {
    const parsed = parseStaticFilePath(path);
    if (parsed.error || !parsed.path) {
      setPathError(parsed.error ?? "Введите корректный маршрут.");
      return;
    }
    const duplicate = routes.some((item) =>
      item.id !== route.id &&
      comparableRootPath(item.path) === comparableRootPath(parsed.path!),
    );
    if (duplicate) {
      setPathError("Такой маршрут уже используется.");
      return;
    }
    update((current) => ({
      ...current,
      routeTree: updateRouteNode(current.routeTree, route.id, (node) => ({
        ...node,
        type: "path",
        path: parsed.path,
        pageId: undefined,
        value: undefined,
        name: undefined,
        pattern: undefined,
      })),
      formatVersion: 3,
    }));
    setPathError("");
  };

  const selectFile = (file: Media) => {
    update((current) => ({
      ...current,
      routeTree: updateRouteNode(current.routeTree, route.id, (node) => ({ ...node, fileId: file.id })),
      formatVersion: 3,
    }));
  };

  const remove = async () => {
    const accepted = await confirm({
      title: "Удалить файловый маршрут?",
      description: `URL ${route.path} больше не будет отдавать файл. Сам файл останется в медиатеке.`,
      confirmLabel: "Удалить маршрут",
      destructive: true,
    });
    if (!accepted) return;
    update((current) => ({ ...current, routeTree: removeRouteBranch(current.routeTree, route.id), formatVersion: 3 }));
    toast.success("Файловый маршрут удалён из рабочей копии");
    navigate(listURL);
  };

  const file = media.data ?? null;
  return <div className="h-full overflow-y-auto bg-background">
    <div className="mx-auto w-full max-w-5xl px-4 py-5 sm:px-7 sm:py-7">
      <Link to={listURL} className="inline-flex min-h-11 items-center gap-2 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-4" />Все страницы и файлы
      </Link>
      <header className="mt-3 flex flex-wrap items-start justify-between gap-4 border-b border-border pb-5">
        <div className="min-w-0">
          <p className="text-xs text-muted-foreground">Файловый маршрут</p>
          <h1 className="mt-1 break-words text-2xl font-semibold tracking-tight">{file?.original_filename ?? "Публичный файл"}</h1>
          <p className="mt-1 font-mono text-sm text-muted-foreground">{route.path}</p>
        </div>
        <Button type="button" variant="ghost" className="min-h-11 text-destructive" onClick={() => void remove()}><Trash2 />Удалить маршрут</Button>
      </header>

      <section className="grid gap-5 border-b border-border py-6">
        <div className="space-y-2">
          <Label htmlFor="file-route-page-path">Маршрут</Label>
          <div className="flex flex-col gap-2 sm:flex-row">
            <Input id="file-route-page-path" className="min-h-11 font-mono" value={path} onChange={(event) => { setPath(event.currentTarget.value); setPathError(""); }} aria-invalid={Boolean(pathError)} aria-describedby={pathError ? "file-route-page-path-error" : undefined} />
            <Button type="button" variant="outline" className="min-h-11 shrink-0" disabled={path === route.path} onClick={applyPath}><Save />Применить</Button>
          </div>
          {pathError
            ? <p id="file-route-page-path-error" role="alert" className="text-xs text-destructive">{pathError}</p>
            : <p className="text-xs text-muted-foreground">Используйте абсолютный фиксированный путь. Параметры маршрута для файлов недоступны.</p>}
        </div>
      </section>

      <section className="space-y-3 py-6">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div><h2 className="font-medium">Файл</h2><p className="mt-1 text-sm text-muted-foreground">Публичный файл из медиатеки будет отдаваться напрямую по этому маршруту.</p></div>
          <Button type="button" variant="outline" className="min-h-11" onClick={() => setPickerOpen(true)}><FileUp />Заменить файл</Button>
        </div>
        {media.isError
          ? <p role="alert" className="border-y border-warning/40 bg-warning/5 py-3 text-sm text-warning">Файл больше недоступен. Выберите другой файл.</p>
          : <dl className="divide-y divide-border border-y border-border text-sm">
            <div className="grid gap-1 py-3 sm:grid-cols-[10rem_minmax(0,1fr)]"><dt className="text-muted-foreground">Имя</dt><dd className="min-w-0 truncate">{file?.original_filename ?? "Загружаем…"}</dd></div>
            <div className="grid gap-1 py-3 sm:grid-cols-[10rem_minmax(0,1fr)]"><dt className="text-muted-foreground">Тип</dt><dd>{file?.content_type ?? "—"}</dd></div>
            <div className="grid gap-1 py-3 sm:grid-cols-[10rem_minmax(0,1fr)]"><dt className="text-muted-foreground">Размер</dt><dd>{file ? formatBytes(file.size_bytes) : "—"}</dd></div>
          </dl>}
      </section>
    </div>
    <MediaPickerDialog open={pickerOpen} onClose={() => setPickerOpen(false)} accept="all" title="Выбрать публичный файл" initialSelected={route.node.fileId ? [route.node.fileId] : []} onSelect={(items) => {
      if (items[0]) selectFile(items[0]);
      setPickerOpen(false);
    }} />
  </div>;
}

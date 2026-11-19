import { useMemo, useState } from "react";
import { ArrowRight, Database, Plus, Search } from "lucide-react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import type { DataModelEntity } from "@/types";
import { useSiteWorkspace } from "./SiteWorkspace";
import { siteContextQuery } from "./site-query";
import {
  addDataModelEntity,
  dataModelEntityBindingImpacts,
  emptyDataModel,
} from "./components/data-model/model-operations";

export * from "./components/data-model/model-operations";

type ModelFilter = "all" | "many" | "singleton";
type ModelSort = "name" | "fields";

export default function DataModelPage() {
  useDocumentTitle("Модель данных");
  const { config, dataModel, updateDataModel } = useSiteWorkspace();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<ModelFilter>("all");
  const [sort, setSort] = useState<ModelSort>("name");
  const [createOpen, setCreateOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [newCardinality, setNewCardinality] = useState<NonNullable<DataModelEntity["cardinality"]>>("many");
  const snapshotId = searchParams.get("snapshot") ?? "";
  const suffix = siteContextQuery(snapshotId, searchParams.get("page_id") ?? "");
  const model = useMemo(() => dataModel ?? emptyDataModel(), [dataModel]);
  const fieldCount = model.entities.reduce((count, entity) => count + entity.fields.length, 0);

  const groups = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase("ru");
    const models = model.entities
      .filter((entity) => filter === "all" || (entity.cardinality ?? "many") === filter)
      .filter((entity) => !needle || [entity.name, ...entity.fields.map((field) => field.name)]
        .join(" ")
        .toLocaleLowerCase("ru")
        .includes(needle))
      .sort((left, right) => sort === "fields"
        ? right.fields.length - left.fields.length || left.name.localeCompare(right.name, "ru")
        : left.name.localeCompare(right.name, "ru"));
    return [
      { key: "many", label: "Коллекции", entities: models.filter((entity) => (entity.cardinality ?? "many") === "many") },
      { key: "singleton", label: "Одиночные настройки", entities: models.filter((entity) => entity.cardinality === "singleton") },
    ].filter((group) => group.entities.length > 0);
  }, [filter, model.entities, query, sort]);
  const resultCount = groups.reduce((count, group) => count + group.entities.length, 0);

  const openCreate = () => {
    setNewName("");
    setNewCardinality("many");
    setCreateOpen(true);
  };
  const createModel = () => {
    const name = newName.trim();
    if (!name) return;
    const id = crypto.randomUUID();
    let createdKey = "";
    updateDataModel((current) => {
      const next = addDataModelEntity(current, () => id, name, newCardinality);
      createdKey = next.entities.find((entity) => entity.id === id)?.key ?? "";
      return next;
    });
    setCreateOpen(false);
    if (createdKey) navigate(`/site/data-model/${createdKey}${suffix}`);
  };

  return <div className="h-full min-h-0 overflow-y-auto bg-background">
    <div className="mx-auto w-full max-w-6xl px-4 py-5 sm:px-7 sm:py-7">
      <header className="flex flex-wrap items-start justify-between gap-4 border-b border-border pb-5">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Модель данных</h1>
          <p className="mt-1 text-sm text-muted-foreground">Модели и поля общие для всех страниц сайта.</p>
        </div>
        <Button type="button" className="min-h-11" onClick={openCreate}><Plus />Новая модель</Button>
      </header>

      <section aria-label="Сводка модели данных" className="grid border-b border-border sm:grid-cols-2">
        <SummaryMetric label="Модели" value={model.entities.length} />
        <SummaryMetric label="Поля" value={fieldCount} />
      </section>

      <div className="grid gap-2 border-b border-border py-4 md:grid-cols-[minmax(0,1fr)_12rem_12rem]">
        <label className="relative min-w-0">
          <span className="sr-only">Поиск моделей и полей</span>
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input type="search" className="min-h-11 pl-9" value={query} onChange={(event) => setQuery(event.currentTarget.value)} placeholder="Название модели или поля" />
        </label>
        <label>
          <span className="sr-only">Тип моделей</span>
          <select className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={filter} onChange={(event) => setFilter(event.currentTarget.value as ModelFilter)}>
            <option value="all">Все типы</option>
            <option value="many">Коллекции</option>
            <option value="singleton">Одиночные настройки</option>
          </select>
        </label>
        <label>
          <span className="sr-only">Сортировка моделей</span>
          <select className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={sort} onChange={(event) => setSort(event.currentTarget.value as ModelSort)}>
            <option value="name">По названию</option>
            <option value="fields">По числу полей</option>
          </select>
        </label>
      </div>

      {resultCount === 0 ? <section className="py-16 text-center">
        <Database className="mx-auto size-7 text-muted-foreground" />
        <h2 className="mt-3 font-medium">{model.entities.length === 0 ? "Моделей пока нет" : query ? "Ничего не найдено" : "Нет моделей выбранного типа"}</h2>
        <p className="mt-1 text-sm text-muted-foreground">{model.entities.length === 0 ? "Создайте первую модель и добавьте в неё поля." : query ? "Измените поисковый запрос." : "Измените фильтр."}</p>
        {model.entities.length === 0 && <Button type="button" className="mt-5 min-h-11" onClick={openCreate}><Plus />Новая модель</Button>}
      </section> : <div className="divide-y divide-border">
        {groups.map((group) => <section key={group.key} className="py-5" aria-labelledby={`model-group-${group.key}`}>
          <div className="mb-2 flex items-center justify-between">
            <h2 id={`model-group-${group.key}`} className="text-xs font-medium text-muted-foreground">{group.label}</h2>
            <span className="text-xs text-muted-foreground">{group.entities.length}</span>
          </div>
          <div className="divide-y divide-border/70">
            {group.entities.map((entity) => {
              const impacts = dataModelEntityBindingImpacts(config, model, entity.id);
              return <Link
                key={entity.id}
                to={`/site/data-model/${encodeURIComponent(entity.key)}${suffix}`}
                className="group grid min-h-16 min-w-0 gap-2 px-1 py-3 hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:grid-cols-[minmax(0,1fr)_9rem_11rem_auto] sm:items-center"
              >
                <span className="min-w-0">
                  <span className="block truncate text-sm font-medium">{entity.name}</span>
                  <span className="mt-0.5 block truncate text-xs text-muted-foreground">{entity.fields.slice(0, 3).map((field) => field.name).join(" · ") || "Полей пока нет"}</span>
                </span>
                <span className="text-xs text-muted-foreground">{entity.cardinality === "singleton" ? "Одна запись" : "Коллекция"}</span>
                <span className="text-xs text-muted-foreground">
                  {entity.fields.length} {entity.fields.length === 1 ? "поле" : entity.fields.length < 5 ? "поля" : "полей"}
                  {impacts.length > 0 ? ` · ${impacts.length} привязок` : ""}
                </span>
                <span className="flex items-center justify-end gap-2 text-sm text-muted-foreground group-hover:text-foreground">Открыть<ArrowRight className="size-4 transition-transform group-hover:translate-x-0.5" /></span>
              </Link>;
            })}
          </div>
        </section>)}
      </div>}
    </div>

    <Dialog open={createOpen} onOpenChange={setCreateOpen}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Новая модель</DialogTitle>
          <DialogDescription>Модель описывает структуру одной сущности и набор редактируемых полей.</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <label className="block space-y-1.5 text-sm">
            <span className="font-medium">Название</span>
            <Input autoFocus className="min-h-11" value={newName} maxLength={120} onChange={(event) => setNewName(event.currentTarget.value)} placeholder="Например, Товары" />
          </label>
          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">Тип</legend>
            <label className="flex min-h-11 cursor-pointer items-start gap-3 rounded-md border border-border p-3">
              <input className="mt-1" type="radio" name="model-cardinality" checked={newCardinality === "many"} onChange={() => setNewCardinality("many")} />
              <span><span className="block text-sm font-medium">Коллекция</span><span className="mt-0.5 block text-xs text-muted-foreground">Список записей: товары, контакты, новости.</span></span>
            </label>
            <label className="flex min-h-11 cursor-pointer items-start gap-3 rounded-md border border-border p-3">
              <input className="mt-1" type="radio" name="model-cardinality" checked={newCardinality === "singleton"} onChange={() => setNewCardinality("singleton")} />
              <span><span className="block text-sm font-medium">Одна запись</span><span className="mt-0.5 block text-xs text-muted-foreground">Единый набор настроек: шапка, подвал, главный экран.</span></span>
            </label>
          </fieldset>
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" className="min-h-11" onClick={() => setCreateOpen(false)}>Отмена</Button>
          <Button type="button" className="min-h-11" disabled={!newName.trim()} onClick={createModel}>Создать</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>;
}

function SummaryMetric({ label, value }: { label: string; value: number }) {
  return <div className="border-b border-border py-4 sm:border-b-0 sm:border-r sm:px-4 sm:last:border-r-0">
    <p className="text-xl font-semibold">{value}</p>
    <p className="text-xs text-muted-foreground">{label}</p>
  </div>;
}

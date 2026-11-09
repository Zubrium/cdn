import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { BackendRequestSourceKind, BackendValueBinding, FeatureRequestDestination, FeatureRequestSlot, PlacementBackendRequest, PublicSnapshotConstant, SiteConfig, SitePlacement } from "@/types";
import type { SchemaField } from "./schema";

function argumentFitsRequest(schema: SchemaField, destination: FeatureRequestDestination): boolean {
  if (destination.type === "array") return schema.type === "array" ? schema.items?.type === destination.itemsType : schema.type === destination.itemsType;
  if (schema.type === "array") return false;
  return schema.type === destination.type || (destination.type === "number" && schema.type === "integer");
}

function constantFitsRequest(constant: PublicSnapshotConstant, destination: FeatureRequestDestination): boolean {
  if (destination.type === "array") return constant.type === "string-array" ? destination.itemsType === "string" : constant.type === destination.itemsType;
  if (constant.type === "string-array") return false;
  return constant.type === destination.type || (destination.type === "number" && constant.type === "integer");
}

function RequestDestinationEditor({ destination, request, fields, captures, config, onChange }: {
  destination: FeatureRequestDestination;
  request: PlacementBackendRequest;
  fields: Record<string, SchemaField>;
  captures: string[];
  config: SiteConfig;
  onChange: (value: PlacementBackendRequest) => void;
}) {
  const binding = request.bindings?.find((item) => item.destination.kind === destination.kind && item.destination.key === destination.key);
  const sourceKind = binding?.source.kind ?? "";
  const patchBinding = (kind: BackendRequestSourceKind, key: string) => {
    const other = (request.bindings ?? []).filter((item) => item.destination.kind !== destination.kind || item.destination.key !== destination.key);
    const nextBinding: BackendValueBinding = { destination: { kind: destination.kind, key: destination.key }, source: { kind, key } };
    onChange({ ...request, bindings: [...other, nextBinding] });
  };
  const clearBinding = () => onChange({ ...request, bindings: (request.bindings ?? []).filter((item) => item.destination.kind !== destination.kind || item.destination.key !== destination.key) });
  const compatibleArguments = Object.entries(fields).filter(([, schema]) => argumentFitsRequest(schema, destination));
  const compatibleConstants = (config.publicConstants ?? []).filter((constant) => constantFitsRequest(constant, destination));
  return <div className="grid min-w-0 gap-2 rounded-md border border-border p-3 sm:grid-cols-[minmax(9rem,0.8fr)_minmax(0,1fr)]">
    <div><p className="text-sm font-medium">{destination.label}{destination.required ? " · обязательно" : ""}</p><p className="mt-1 text-xs text-muted-foreground">{destination.kind === "path" ? `Сегмент пути · {${destination.key}}` : destination.kind === "query" ? `Query · ${destination.key}` : destination.kind === "body" ? `Поле JSON body · ${destination.key}` : `Заголовок · ${destination.key}`}{destination.type === "array" ? " · массив, повторяющиеся query-ключи сохраняются" : ""}</p></div>
    <div className="grid min-w-0 gap-2">
      <label className="space-y-1 text-sm"><span>Источник</span><select aria-label={`Источник ${destination.label}`} className="h-11 w-full rounded-md border border-input bg-background px-3" value={sourceKind} onChange={(event) => {
        const kind = event.currentTarget.value as BackendRequestSourceKind | "";
        if (!kind) { clearBinding(); return; }
        let key = "";
        if (kind === "route") key = captures[0] ?? "";
        if (kind === "argument") key = compatibleArguments[0]?.[0] ?? "";
        if (kind === "constant") key = compatibleConstants[0]?.key ?? "";
        patchBinding(kind, key);
      }}><option value="">Не подключать</option>{destination.sources.includes("route") && <option value="route" disabled={!captures.length}>Параметр маршрута</option>}{destination.sources.includes("query") && <option value="query">Входной query</option>}{destination.sources.includes("argument") && <option value="argument" disabled={!compatibleArguments.length}>Аргумент фичи</option>}{destination.sources.includes("constant") && <option value="constant" disabled={!compatibleConstants.length}>Константа снапшота</option>}</select></label>
      {sourceKind === "route" && <label className="space-y-1 text-sm"><span>Параметр пути</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={binding?.source.key ?? ""} onChange={(event) => patchBinding("route", event.currentTarget.value)}>{captures.map((capture) => <option key={capture} value={capture}>{capture}</option>)}</select></label>}
      {sourceKind === "query" && <label className="space-y-1 text-sm"><span>Ключ входного query</span><Input className="min-h-11" value={binding?.source.key ?? ""} onChange={(event) => patchBinding("query", event.currentTarget.value)} placeholder="filter" /></label>}
      {sourceKind === "argument" && <label className="space-y-1 text-sm"><span>Аргумент фичи</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={binding?.source.key ?? ""} onChange={(event) => patchBinding("argument", event.currentTarget.value)}>{compatibleArguments.map(([key, schema]) => <option key={key} value={key}>{schema.title ?? key}</option>)}</select></label>}
      {sourceKind === "constant" && <label className="space-y-1 text-sm"><span>Публичная константа</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={binding?.source.key ?? ""} onChange={(event) => patchBinding("constant", event.currentTarget.value)}>{compatibleConstants.map((constant) => <option key={constant.key} value={constant.key}>{constant.label}</option>)}</select></label>}
    </div>
  </div>;
}

function BackendRequestEditor({ slot, placement, fields, captures, config, onChange }: {
  slot: FeatureRequestSlot;
  placement: SitePlacement;
  fields: Record<string, SchemaField>;
  captures: string[];
  config: SiteConfig;
  onChange: (value: SitePlacement) => void;
}) {
  const current = placement.backendRequests?.[slot.key];
  const request: PlacementBackendRequest = current ?? { path: slot.defaultPath, bindings: [] };
  const setRequest = (next?: PlacementBackendRequest) => {
    const backendRequests = { ...(placement.backendRequests ?? {}) };
    if (next) backendRequests[slot.key] = next;
    else delete backendRequests[slot.key];
    onChange({ ...placement, backendRequests: Object.keys(backendRequests).length ? backendRequests : undefined });
  };
  return <fieldset className="space-y-3 rounded-md border border-border p-3">
    <legend className="px-1 text-sm font-medium">{slot.label}</legend>
    <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)]">
      <label className="space-y-1.5 text-sm"><span>Публичный backend</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={request.backendId ?? ""} onChange={(event) => setRequest({ ...request, backendId: event.currentTarget.value || undefined })}><option value="">Текущий сайт · same-origin</option>{(config.backends ?? []).map((backend) => <option key={backend.id} value={backend.id}>{backend.name || backend.baseUrl}</option>)}</select><span className="block text-xs text-muted-foreground">Внешний API должен разрешать origin сайта в CORS.</span></label>
      <label className="space-y-1.5 text-sm"><span>{slot.pathLabel || "Путь запроса"}</span><Input className="min-h-11" value={request.path} onChange={(event) => setRequest({ ...request, path: event.currentTarget.value })} placeholder={slot.defaultPath} /><span className="block text-xs text-muted-foreground">Только относительный путь. Именованные сегменты задаются фигурными скобками.</span></label>
    </div>
    <div className="space-y-2">{slot.destinations.map((destination) => <RequestDestinationEditor key={`${destination.kind}:${destination.key}`} destination={destination} request={request} fields={fields} captures={captures} config={config} onChange={setRequest} />)}</div>
    {!slot.destinations.some((destination) => destination.required) && <Button type="button" variant="outline" className="min-h-11" onClick={() => setRequest(undefined)}>Не использовать этот запрос</Button>}
  </fieldset>;
}



export function BackendRequestsEditor({ slots, placement, fields, captures, config, onChange }: {
  slots: FeatureRequestSlot[];
  placement: SitePlacement;
  fields: Record<string, SchemaField>;
  captures: string[];
  config: SiteConfig;
  onChange: (value: SitePlacement) => void;
}) {
  if (!slots.length) return null;
  return <fieldset className="space-y-3 border-t border-border pt-3">
    <legend className="px-1 text-sm font-medium">Публичные запросы фичи</legend>
    <p className="text-xs text-muted-foreground">Фича выполняет HTTP-запрос сама. Движок передаёт только публичный origin, относительный путь и типизированные связи.</p>
    {slots.map((slot) => <BackendRequestEditor key={slot.key} slot={slot} placement={placement} fields={fields} captures={captures} config={config} onChange={onChange} />)}
  </fieldset>;
}

import type { DataInputBinding, DataModelEntity, DetailSEOFieldBindings, FeatureManifest, SiteConfig, SitePage, SitePlacement } from "@/types";
import { DataInputBindingsEditor } from "../../components/DataInputBindingsEditor";
import type { RouteDescriptor } from "../../route-tree";
import { Input } from "@/components/ui/input";

export function updateDetailSEOField(
  bindings: DataInputBinding[],
  inputKey: string,
  role: keyof DetailSEOFieldBindings,
  fieldId: string,
): DataInputBinding[] {
  return bindings.map((binding) => {
    if (binding.inputKey !== inputKey) return binding;
    const detailSEO = { ...(binding.detailSEO ?? {}) };
    if (fieldId) detailSEO[role] = fieldId;
    else delete detailSEO[role];
    const configured = Object.values(detailSEO).some(Boolean);
    return { ...binding, detailSEO: configured ? detailSEO : undefined };
  });
}

export function DetailSEOFieldsEditor({ binding, entity, disabled = false, onChange }: {
  binding: DataInputBinding;
  entity: DataModelEntity;
  disabled?: boolean;
  onChange: (binding: DataInputBinding) => void;
}) {
  const fields: DetailSEOFieldBindings = binding.detailSEO ?? {};
  const textFields = entity.fields.filter((field) => field.type === "text" || field.type === "longText");
  const mediaFields = entity.fields.filter((field) => field.type === "media");
  const controls: Array<{ role: keyof DetailSEOFieldBindings; label: string; fields: DataModelEntity["fields"] }> = [
    { role: "titleFieldId", label: "Заголовок страницы", fields: textFields },
    { role: "descriptionFieldId", label: "Описание страницы", fields: textFields },
    { role: "ogTitleFieldId", label: "Заголовок ссылки", fields: textFields },
    { role: "ogDescriptionFieldId", label: "Описание ссылки", fields: textFields },
    { role: "ogImageFieldId", label: "Изображение ссылки", fields: mediaFields },
  ];
  const change = (role: keyof DetailSEOFieldBindings, fieldId: string) => {
    onChange(updateDetailSEOField([binding], binding.inputKey, role, fieldId)[0]!);
  };
  return <fieldset className="grid gap-3 border-t border-border pt-3 sm:grid-cols-2">
    <legend className="px-1 text-sm font-medium">SEO из записи</legend>
    <p className="text-xs text-muted-foreground sm:col-span-2">В HTML подставляются значения опубликованной записи. Пустое поле записи наследует SEO-настройки страницы.</p>
    {disabled && <p role="status" className="text-xs text-warning sm:col-span-2">SEO-поля уже сопоставлены с другой записью этой detail-страницы. Настройте их у выбранного источника.</p>}
    {controls.map(({ role, label, fields: options }) => {
      const selectedField = fields[role] ?? "";
      const compatible = !selectedField || options.some((field) => field.id === selectedField);
      const errorID = `detail-seo-${binding.inputKey}-${role}-error`;
      return <label key={role} className="space-y-1.5 text-sm">
        <span className="text-muted-foreground">{label}</span>
        <select className="h-11 w-full rounded-md border border-input bg-background px-3" disabled={disabled} aria-invalid={!compatible} aria-describedby={compatible ? undefined : errorID} value={selectedField} onChange={(event) => change(role, event.currentTarget.value)}>
          <option value="">Использовать настройки страницы</option>
          {!compatible && <option value={selectedField} disabled>Сохранённое поле отсутствует или несовместимо</option>}
          {options.map((field) => <option key={field.id} value={field.id}>{field.name}</option>)}
        </select>
        {!compatible && <span id={errorID} className="block text-xs text-warning" role="alert">Выберите совместимое поле модели данных.</span>}
      </label>;
    })}
  </fieldset>;
}



export function StructuralBindingsEditor({ page, placement, manifest, config, captures, snapshotId, onChange }: {
  page: SitePage;
  placement: SitePlacement;
  manifest: FeatureManifest;
  config: SiteConfig;
  captures: string[];
  snapshotId: string;
  onChange: (value: SitePlacement) => void;
}) {
  const ports = manifest.dataInputs ?? [];
  return <>
    {ports.length > 0 && <DataInputBindingsEditor ports={ports} dataModel={config.dataModel} bindings={placement.dataBindings ?? []} arguments={placement.arguments} routeCaptures={captures} snapshotId={snapshotId} onChange={(dataBindings) => onChange({ ...placement, dataBindings })} />}
    {page.kind === "detail" && ports.filter((input) => input.kind === "record").map((input) => {
      const binding = (placement.dataBindings ?? []).find((item) => item.inputKey === input.key && item.lookup);
      const entity = binding && config.dataModel?.entities.find((item) => item.id === binding.entityId);
      if (!binding || !entity) return null;
      const configuredElsewhere = page.placements.some((candidate) => candidate.dataBindings?.some((item) =>
        Object.values(item.detailSEO ?? {}).some(Boolean) && !(candidate.id === placement.id && item.inputKey === input.key),
      ));
      return <DetailSEOFieldsEditor key={`${placement.id}:${input.key}:seo`} binding={binding} entity={entity} disabled={configuredElsewhere} onChange={(next) => onChange({
        ...placement,
        dataBindings: (placement.dataBindings ?? []).map((item) => item.inputKey === input.key ? next : item),
      })} />;
    })}
  </>;
}

export function DetailLinkEditor({ placement, formatVersion, fields, detailRoutes, onChange }: {
  placement: SitePlacement;
  formatVersion: FeatureManifest["formatVersion"];
  fields: Array<{ field: string; label: string }>;
  detailRoutes: RouteDescriptor[];
  onChange: (value: SitePlacement) => void;
}) {
  if (formatVersion !== 2 || fields.length === 0) return null;
  return <fieldset className="grid gap-3 border-t border-border pt-3 sm:grid-cols-2">
    <legend className="px-1 text-sm font-medium">Ссылка на detail-страницу</legend>
    <label className="space-y-1.5 text-sm"><span className="text-muted-foreground">Маршрут карточки товара</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={placement.detailLink?.routeId ?? ""} onChange={(event) => {
      const route = detailRoutes.find((item) => item.id === event.currentTarget.value);
      onChange({ ...placement, detailLink: route ? { routeId: route.id, field: placement.detailLink?.field ?? fields[0]!.field, pathTemplate: route.path } : undefined });
    }}><option value="">Не добавлять ссылку</option>{detailRoutes.map((route) => <option key={route.id} value={route.id}>{route.path}</option>)}</select></label>
    <label className="space-y-1.5 text-sm"><span className="text-muted-foreground">Поле маршрута</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={placement.detailLink?.field ?? ""} disabled={!placement.detailLink} onChange={(event) => onChange({ ...placement, detailLink: placement.detailLink ? { ...placement.detailLink, field: event.currentTarget.value } : undefined })}><option value="">Выберите поле</option>{fields.map((item) => <option key={item.field} value={item.field}>{item.label}</option>)}</select></label>
    {placement.detailLink && <label className="space-y-1.5 text-sm sm:col-span-2"><span className="text-muted-foreground">Шаблон ссылки</span><Input className="min-h-11" value={placement.detailLink.pathTemplate} onChange={(event) => onChange({ ...placement, detailLink: placement.detailLink ? { ...placement.detailLink, pathTemplate: event.currentTarget.value } : undefined })} /></label>}
    {!detailRoutes.length && <p className="text-xs text-muted-foreground sm:col-span-2">Создайте detail-маршрут, чтобы связать карточку с записью модели.</p>}
  </fieldset>;
}

import { useState } from "react";
import { PageHeader, useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { MediaPickerDialog } from "@/domains/media/MediaPickerDialog";
import { useSiteWorkspace } from "@/pages/site/SiteWorkspace";
import type { SiteMetadata } from "@/types";

export default function SiteMetadataPage() {
  useDocumentTitle("Настройки сайта");
  const { metadata, updateMetadata } = useSiteWorkspace();
  const [picker, setPicker] = useState<"faviconMediaId" | "ogImageMediaId" | null>(null);
  const update = (key: keyof SiteMetadata, value: string | boolean) => updateMetadata((current) => ({ ...current, [key]: value }));
  return <div className="page-grid min-w-0">
    <PageHeader title="Настройки сайта" description="Общие параметры сайта и его поискового представления. Сохраните изменения общей кнопкой редактора." />
    <fieldset className="grid min-w-0 gap-4 rounded-xl border border-border bg-card p-4 sm:max-w-3xl">
      <legend className="sr-only">Общие настройки сайта</legend>
      {([ ["siteName", "Название сайта"], ["description", "Описание сайта"], ["ogTitle", "Заголовок ссылки"], ["ogDescription", "Описание ссылки"] ] as const).map(([key, label]) => <div className="space-y-2" key={key}><Label htmlFor={`metadata-${key}`}>{label}</Label><Input id={`metadata-${key}`} value={metadata[key] ?? ""} onChange={(event) => update(key, event.currentTarget.value)} /></div>)}
      <div className="space-y-2"><Label htmlFor="canonical-domain">Canonical-домен</Label><Input id="canonical-domain" type="url" value={metadata.canonicalDomain ?? ""} placeholder="https://example.com" onChange={(event) => update("canonicalDomain", event.currentTarget.value)} /><p className="text-xs text-muted-foreground">Маршрутизацию домена настраивает gateway.</p></div>
      <div className="space-y-2"><Label htmlFor="site-language">Язык сайта</Label><Input id="site-language" value={metadata.language ?? ""} placeholder="ru" onChange={(event) => update("language", event.currentTarget.value)} /></div>
      {([ ["faviconMediaId", "Иконка сайта"], ["ogImageMediaId", "Изображение ссылки"] ] as const).map(([key, label]) => <div key={key} className="flex flex-wrap items-center gap-2"><span className="text-sm">{label}</span><Button type="button" variant="outline" onClick={() => setPicker(key)}>{metadata[key] ? "Заменить файл" : "Выбрать файл"}</Button>{metadata[key] && <Button type="button" variant="ghost" onClick={() => update(key, "")}>Убрать</Button>}</div>)}
      <label className="flex items-center gap-2"><input type="checkbox" checked={metadata.noIndex ?? false} onChange={(event) => update("noIndex", event.currentTarget.checked)} />Запретить индексацию</label>
    </fieldset>
    <MediaPickerDialog open={picker !== null} onClose={() => setPicker(null)} accept="image" onSelect={(items) => { if (picker && items[0]) update(picker, items[0].id); setPicker(null); }} />
  </div>;
}

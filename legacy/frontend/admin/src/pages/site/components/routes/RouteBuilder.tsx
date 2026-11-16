import { useState } from "react";
import { WandSparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import {
  appendRouteSegment,
  buildRouteSegment,
  routeRegexPresets,
  type RouteRegexPreset,
  type RouteSegmentDraft,
  type RouteSegmentKind,
} from "./route-builder";

export function RouteBuilder({ isRoot, value, onChange }: { isRoot: boolean; value: string; onChange: (value: string) => void }) {
  const [draft, setDraft] = useState<RouteSegmentDraft>({
    kind: "static",
    staticValue: "",
    name: "slug",
    preset: "slug",
    customPattern: "",
  });
  const [error, setError] = useState("");
  const [example, setExample] = useState("");
  const patch = (next: Partial<RouteSegmentDraft>) => {
    setDraft((current) => ({ ...current, ...next }));
    setError("");
  };
  const apply = () => {
    const result = buildRouteSegment(draft);
    if (!result.value) {
      setError(result.error ?? "Не удалось собрать сегмент.");
      return;
    }
    onChange(isRoot ? appendRouteSegment(value, result.value) : result.value);
    setExample(result.example ?? "");
    setError("");
  };

  return <details className="rounded-md border border-border bg-muted/20 p-3">
    <summary className="flex cursor-pointer list-none items-center gap-2 text-sm font-medium">
      <WandSparkles className="size-4 text-primary" aria-hidden="true" />
      Собрать {isRoot ? "сегмент маршрута" : "шаблон"} без знания регулярных выражений
    </summary>
    <div className="mt-3 grid gap-3 sm:grid-cols-2">
      <label className="space-y-1.5 text-sm">
        <span>Тип сегмента</span>
        <select className="h-11 w-full rounded-md border border-input bg-background px-3" value={draft.kind} onChange={(event) => patch({ kind: event.currentTarget.value as RouteSegmentKind })}>
          <option value="static">Обычный текст</option>
          <option value="param">Параметр пути</option>
          <option value="regex">Шаблон с проверкой</option>
          <option value="catchAll" disabled={!isRoot}>Любой остаток пути</option>
        </select>
      </label>
      {draft.kind === "static" ? <div className="space-y-1.5">
        <Label htmlFor="route-builder-static">Текст в URL</Label>
        <Input id="route-builder-static" className="min-h-11" value={draft.staticValue} onChange={(event) => patch({ staticValue: event.currentTarget.value })} placeholder="products" />
      </div> : draft.kind === "regex" ? <>
        <div className="space-y-1.5">
          <Label htmlFor="route-builder-name">Имя извлечённого значения</Label>
          <Input id="route-builder-name" className="min-h-11" value={draft.name} onChange={(event) => patch({ name: event.currentTarget.value })} placeholder="slug" />
        </div>
        <label className="space-y-1.5 text-sm">
          <span>Что разрешено в сегменте</span>
          <select className="h-11 w-full rounded-md border border-input bg-background px-3" value={draft.preset} onChange={(event) => patch({ preset: event.currentTarget.value as RouteRegexPreset })}>
            {routeRegexPresets.map((preset) => <option key={preset.value} value={preset.value}>{preset.label}</option>)}
          </select>
        </label>
        {draft.preset === "custom" && <div className="space-y-1.5 sm:col-span-2">
          <Label htmlFor="route-builder-pattern">Своё правило для одного сегмента</Label>
          <Input id="route-builder-pattern" className="min-h-11 font-mono text-sm" value={draft.customPattern} onChange={(event) => patch({ customPattern: event.currentTarget.value })} placeholder="[A-Z]{2,4}" />
          <p className="text-xs text-muted-foreground">Экспертный режим. Символ / использовать нельзя, пустой сегмент запрещён.</p>
        </div>}
      </> : <div className="space-y-1.5">
        <Label htmlFor="route-builder-name">Имя извлечённого значения</Label>
        <Input id="route-builder-name" className="min-h-11" value={draft.name} onChange={(event) => patch({ name: event.currentTarget.value })} placeholder={draft.kind === "catchAll" ? "path" : "slug"} />
      </div>}
    </div>
    <div className="mt-3 flex flex-wrap items-center gap-2">
      <Button type="button" variant="outline" className="min-h-11" onClick={apply}>{isRoot ? "Добавить сегмент в URL" : "Подставить сегмент"}</Button>
      {draft.kind === "regex" && <p className="text-xs text-muted-foreground">Пример подходящего значения: {routeRegexPresets.find((preset) => preset.value === draft.preset)?.example || "задайте своё правило"}</p>}
      {example && <p role="status" className="text-xs text-success">Пример извлечённого значения: {example}</p>}
    </div>
    {error && <p role="alert" className="mt-2 text-sm text-destructive">{error}</p>}
  </details>;
}

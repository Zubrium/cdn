import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { publicMediaPath } from "@/api";
import { useConfirm } from "@/components/common/confirm";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { MediaPickerDialog } from "@/domains/media/MediaPickerDialog";
import type { DesignFont } from "@/types";
import { FontSample } from "./FontSample";
import type { StructuredDesignSystem } from "./types";

const fontWeights = ["100", "200", "300", "400", "500", "600", "700", "800", "900", "100 900"];
const selectClass = "h-11 max-w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring";

type Props = {
  theme: StructuredDesignSystem;
  update: (fn: (current: StructuredDesignSystem) => StructuredDesignSystem) => void;
};

export function BrandAssetEditor({ theme, update }: Props) {
  const confirm = useConfirm();
  const [picker, setPicker] = useState<"font" | "logo" | null>(null);
  const [fontFamily, setFontFamily] = useState("");

  const updateFont = (index: number, patch: Partial<DesignFont>) => update((current) => ({
    ...current,
    fonts: current.fonts.map((font, i) => i === index ? { ...font, ...patch } : font),
  }));

  const removeFont = async (index: number) => {
    const font = theme.fonts[index];
    if (!font) return;
    const values = [
      ...theme.baseTokens.map((token) => token.value),
      ...Object.values(theme.featureValues),
      ...theme.breakpoints.flatMap((breakpoint) => [...Object.values(breakpoint.baseValues), ...Object.values(breakpoint.featureValues)]),
    ];
    if (values.some((value) => value.font === font.family) && theme.fonts.filter((item) => item.family === font.family).length === 1) {
      toast.error("Сначала выберите другой шрифт в использующих его токенах.");
      return;
    }
    const accepted = await confirm({
      title: `Удалить шрифт ${font.family}?`,
      description: "Файл останется в медиатеке. Удаляется подключение шрифта к новой редакции дизайна.",
      confirmLabel: "Удалить подключение",
      destructive: true,
    });
    if (accepted) update((current) => ({ ...current, fonts: current.fonts.filter((_, i) => i !== index) }));
  };

  return (
    <section className="space-y-5" aria-label="Шрифты и бренд">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="font-medium">Логотип</h2>
        <Button variant="outline" onClick={() => setPicker("logo")}>Выбрать изображение</Button>
        {theme.brandLogoMediaId && (
          <>
            <img alt="Логотип сайта" src={publicMediaPath(theme.brandLogoMediaId)} className="h-10 max-w-40 rounded-md bg-muted-foreground/75 object-contain p-1.5" />
            <Button variant="ghost" onClick={() => update((current) => ({ ...current, brandLogoMediaId: undefined }))}>Убрать логотип</Button>
          </>
        )}
      </div>
      <div className="border-t border-border pt-4">
        <h2 className="font-medium">Шрифты</h2>
        <div className="mt-3 flex flex-wrap items-end gap-2">
          <div className="w-full space-y-1 sm:w-auto">
            <Label htmlFor="design-font-family">Название семейства шрифта</Label>
            <Input id="design-font-family" value={fontFamily} onChange={(event) => setFontFamily(event.target.value)} className="min-h-11 sm:max-w-72" />
          </div>
          <Button variant="outline" disabled={!fontFamily.trim()} onClick={() => setPicker("font")}><Plus />Добавить WOFF2</Button>
        </div>
        <ul className="mt-3 divide-y divide-border">
          {theme.fonts.map((font, index) => (
            <li key={`${font.family}-${font.mediaId}-${index}`} className="flex min-w-0 flex-wrap items-start gap-3 py-3">
              <div className="min-w-0 basis-full sm:flex-1">
                <p className="break-words text-sm font-medium">{font.family}</p>
                <FontSample font={font} />
              </div>
              <div className="flex min-w-0 flex-wrap gap-2">
                <select aria-label={`Начертание ${font.family}`} value={font.weight} onChange={(event) => updateFont(index, { weight: event.target.value })} className={selectClass}>
                  {!fontWeights.includes(font.weight) && <option value={font.weight}>{font.weight.replace(" ", "–")}</option>}
                  {fontWeights.map((weight) => <option key={weight} value={weight}>{weight === "100 900" ? "Переменное 100–900" : weight}</option>)}
                </select>
                <select aria-label={`Стиль ${font.family}`} value={font.style} onChange={(event) => updateFont(index, { style: event.target.value === "italic" ? "italic" : "normal" })} className={selectClass}>
                  <option value="normal">Обычный</option><option value="italic">Курсив</option>
                </select>
              </div>
              <Button variant="ghost" size="icon" aria-label={`Удалить шрифт ${font.family}`} className="min-h-11 min-w-11" onClick={() => void removeFont(index)}><Trash2 /></Button>
            </li>
          ))}
        </ul>
      </div>
      <MediaPickerDialog
        open={picker === "font"}
        onClose={() => setPicker(null)}
        accept="font"
        title="Выбрать файл шрифта"
        onSelect={([file]) => {
          if (file) {
            update((current) => ({ ...current, fonts: [...current.fonts, { family: fontFamily.trim(), mediaId: file.id, weight: "100 900", style: "normal" }] }));
            setFontFamily("");
          }
          setPicker(null);
        }}
      />
      <MediaPickerDialog
        open={picker === "logo"}
        onClose={() => setPicker(null)}
        accept="image"
        title="Выбрать логотип"
        onSelect={([file]) => {
          if (file) update((current) => ({ ...current, brandLogoMediaId: file.id }));
          setPicker(null);
        }}
      />
    </section>
  );
}

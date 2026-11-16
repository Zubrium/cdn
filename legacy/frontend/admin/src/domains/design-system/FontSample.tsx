import { useEffect, useId, useState } from "react";
import { publicMediaPath } from "@/api";
import type { DesignFont } from "@/types";

export function FontSample({ font }: { font: DesignFont }) {
  const previewFamily = `design-font-${useId().replace(/[^a-z0-9-]/gi, "")}`;
  const { mediaId, weight, style, unicodeRange } = font;
  const source = publicMediaPath(mediaId);
  const signature = `${source}|${weight}|${style}|${unicodeRange ?? ""}`;
  const [result, setResult] = useState<{ signature: string; loaded: boolean }>();

  useEffect(() => {
    let active = true;
    let preview: FontFace | undefined;
    async function loadPreview() {
      try {
        const face = new FontFace(previewFamily, `url(${JSON.stringify(source)}) format("woff2")`, { weight, style, unicodeRange });
        await face.load();
        if (active) {
          document.fonts.add(face);
          preview = face;
          setResult({ signature, loaded: true });
        }
      } catch {
        if (active) setResult({ signature, loaded: false });
      }
    }
    void loadPreview();
    return () => {
      active = false;
      if (preview) document.fonts.delete(preview);
    };
  }, [previewFamily, signature, source, weight, style, unicodeRange]);

  const loaded = result?.signature === signature && result.loaded;
  const [minimum, maximum = minimum] = weight.split(" ").map(Number);
  const sampleWeight = Math.min(maximum ?? 400, Math.max(minimum ?? 400, 400));
  return (
    <figure aria-label={`Образец шрифта ${font.family}`} className="mt-1 min-w-0">
      <p className="break-words text-xl" style={loaded ? { fontFamily: previewFamily, fontWeight: sampleWeight, fontStyle: style } : undefined}>
        Aa Бб 123
      </p>
      <p role="status" className="text-xs text-muted-foreground">
        {result?.signature !== signature ? "Загружаем образец…" : !loaded ? "Не удалось загрузить образец шрифта" : ""}
      </p>
    </figure>
  );
}

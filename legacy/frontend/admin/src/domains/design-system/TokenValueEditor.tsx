import { Input, Label } from "@/components/ui/input";
import type { DesignFont } from "@/types";
import type { TokenColor, TokenKind, TokenValue } from "./types";
import { initialTokenValue } from "./types";

const selectClass = "h-10 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring";

function ColorEditor({ id, value, onChange, disabled }: { id: string; value: TokenColor; onChange: (next: TokenColor) => void; disabled?: boolean }) {
  return <div className="flex flex-wrap items-center gap-3">
    <Label htmlFor={`${id}-color`} className="sr-only">Цвет</Label>
    <input id={`${id}-color`} type="color" value={value.hex} onChange={(event) => onChange({ ...value, hex: event.target.value })} disabled={disabled} className="h-10 w-14 cursor-pointer rounded-md border border-input bg-background p-1 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" />
    <Label htmlFor={`${id}-opacity`}>Непрозрачность</Label>
    <input id={`${id}-opacity`} type="range" min="0" max="100" value={value.opacity} onChange={(event) => onChange({ ...value, opacity: event.target.valueAsNumber })} disabled={disabled} className="w-28 accent-primary" />
    <output htmlFor={`${id}-opacity`} className="w-10 text-sm tabular-nums">{value.opacity}%</output>
  </div>;
}

export function TokenValueEditor({ id, kind, value, onChange, fonts = [], disabled }: { id: string; kind: TokenKind; value: TokenValue; onChange: (next: TokenValue) => void; fonts?: DesignFont[]; disabled?: boolean }) {
  const literal = value.baseTokenId ? initialTokenValue(kind) : value;
  if (kind === "color") return <ColorEditor id={id} value={literal.color ?? { hex: "#000000", opacity: 100 }} onChange={(color) => onChange({ color })} disabled={disabled} />;
  if (kind === "font") return <select aria-label="Семейство шрифта" value={literal.font ?? "system-ui"} onChange={(event) => onChange({ font: event.target.value })} disabled={disabled} className={selectClass}>
    <option value="system-ui">Системный</option><option value="sans-serif">Без засечек</option><option value="serif">С засечками</option><option value="monospace">Моноширинный</option>
    {[...new Set(fonts.map((font) => font.family))].map((family) => <option key={family} value={family}>{family}</option>)}
  </select>;
  if (kind === "shadow") {
    const shadow = literal.shadow ?? { x: 0, y: 0, blur: 0, spread: 0, color: { hex: "#000000", opacity: 20 }, inset: false };
    return <fieldset className="space-y-3"><legend className="sr-only">Параметры тени</legend><div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      {([{ key: "x", label: "По горизонтали" }, { key: "y", label: "По вертикали" }, { key: "blur", label: "Размытие" }, { key: "spread", label: "Расширение" }] as const).map(({ key, label }) => <div key={key} className="space-y-1"><Label htmlFor={`${id}-${key}`}>{label}, px</Label><Input id={`${id}-${key}`} type="number" min={key === "blur" ? 0 : undefined} value={shadow[key]} onChange={(event) => onChange({ shadow: { ...shadow, [key]: Number(event.target.value) } })} disabled={disabled} /></div>)}
    </div><ColorEditor id={id} value={shadow.color} onChange={(color) => onChange({ shadow: { ...shadow, color } })} disabled={disabled} />
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={shadow.inset} onChange={(event) => onChange({ shadow: { ...shadow, inset: event.target.checked } })} disabled={disabled} />Внутренняя тень</label>
      <div aria-hidden="true" className="h-12 rounded-lg border border-border bg-muted" style={{ boxShadow: `${shadow.inset ? "inset " : ""}${shadow.x}px ${shadow.y}px ${shadow.blur}px ${shadow.spread}px ${shadow.color.hex}${Math.round(shadow.color.opacity * 2.55).toString(16).padStart(2, "0")}` }} />
    </fieldset>;
  }
  return <div className="flex max-w-sm items-center gap-2"><Label htmlFor={id} className="sr-only">Значение</Label><Input id={id} type="number" step="any" value={literal.number ?? 0} onChange={(event) => onChange({ number: Number(event.target.value), ...(kind === "length" ? { unit: literal.unit ?? "px" } : {}) })} disabled={disabled} />
    {kind === "length" && <select aria-label="Единица измерения" value={literal.unit ?? "px"} onChange={(event) => onChange({ number: literal.number ?? 0, unit: event.target.value })} disabled={disabled} className={selectClass}>{["px", "rem", "em", "%", "vw", "vh", "svh", "ch"].map((unit) => <option key={unit}>{unit}</option>)}</select>}
  </div>;
}

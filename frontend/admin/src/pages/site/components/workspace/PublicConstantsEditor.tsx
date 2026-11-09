import type { PublicConstantType, PublicSnapshotConstant, SiteConfig } from "@/types";
import { useConfirm } from "@/components/common/confirm";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

function constantUsers(config: SiteConfig, key: string): string[] {
  const uses: string[] = [];
  for (const page of config.pages) {
    for (const placement of page.placements) {
      if (Object.values(placement.backendRequests ?? {}).some((request) => request.bindings?.some((binding) => binding.source.kind === "constant" && binding.source.key === key))) {
        uses.push(`${page.name} · ${placement.build.featureKey}`);
      }
    }
  }
  return [...new Set(uses)];
}

function initialValue(type: PublicConstantType): PublicSnapshotConstant["value"] {
  if (type === "boolean") return false;
  if (type === "string-array") return [];
  if (type === "string") return "";
  return 0;
}

function displayValue(constant: PublicSnapshotConstant): string {
  return constant.type === "string-array" && Array.isArray(constant.value)
    ? constant.value.map(String).join(", ")
    : String(constant.value ?? "");
}

export function PublicConstantsEditor({ config, constants, onChange }: {
  config: SiteConfig;
  constants: PublicSnapshotConstant[];
  onChange: (next: PublicSnapshotConstant[]) => void;
}) {
  const confirm = useConfirm();
  const addConstant = () => {
    let suffix = constants.length + 1;
    let key = `publicValue${suffix}`;
    while (constants.some((item) => item.key === key)) key = `publicValue${++suffix}`;
    onChange([...constants, { key, label: "Публичное значение", type: "string", value: "" }]);
  };

  return <div className="grid min-w-0 gap-3 border-t border-border pt-5">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <div>
        <h3 className="text-sm font-medium">Публичные константы снапшота</h3>
        <p className="mt-1 text-xs text-muted-foreground">Эти открытые значения могут попадать в аргументы запросов. Не сохраняйте здесь ключи и пароли.</p>
      </div>
      <Button type="button" variant="outline" className="min-h-11" onClick={addConstant}>Добавить значение</Button>
    </div>
    {constants.map((constant, index) => {
      const users = constantUsers(config, constant.key);
      const updateConstant = (patch: Partial<PublicSnapshotConstant>) => onChange(constants.map((item) => item === constant ? { ...item, ...patch } : item));
      return <fieldset key={`${constant.key}:${index}`} className="grid min-w-0 gap-3 rounded-lg border border-border bg-card p-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_9rem_minmax(0,1.3fr)_auto]">
        <legend className="sr-only">Публичная константа</legend>
        <label className="space-y-1.5 text-sm">
          <span>Ключ</span>
          <Input className="min-h-11" value={constant.key} disabled={users.length > 0} onChange={(event) => updateConstant({ key: event.currentTarget.value })} />
        </label>
        <label className="space-y-1.5 text-sm">
          <span>Название</span>
          <Input className="min-h-11" value={constant.label} onChange={(event) => updateConstant({ label: event.currentTarget.value })} />
        </label>
        <label className="space-y-1.5 text-sm">
          <span>Тип</span>
          <select className="h-11 w-full rounded-md border border-input bg-background px-3" value={constant.type} onChange={(event) => {
            const type = event.currentTarget.value as PublicConstantType;
            updateConstant({ type, value: initialValue(type) });
          }}>
            <option value="string">Текст</option>
            <option value="integer">Целое число</option>
            <option value="number">Число</option>
            <option value="boolean">Да/нет</option>
            <option value="string-array">Список текстов</option>
          </select>
        </label>
        <label className="space-y-1.5 text-sm">
          <span>Значение</span>
          {constant.type === "boolean" ? <select className="h-11 w-full rounded-md border border-input bg-background px-3" value={String(constant.value)} onChange={(event) => updateConstant({ value: event.currentTarget.value === "true" })}>
            <option value="true">Да</option>
            <option value="false">Нет</option>
          </select> : <Input className="min-h-11" type={constant.type === "integer" || constant.type === "number" ? "number" : "text"} value={displayValue(constant)} onChange={(event) => {
            const raw = event.currentTarget.value;
            let value: PublicSnapshotConstant["value"] = raw;
            if (constant.type === "integer") value = raw === "" ? "" : Number.parseInt(raw, 10);
            else if (constant.type === "number") value = raw === "" ? "" : Number(raw);
            else if (constant.type === "string-array") value = raw === "" ? [] : raw.split(",").map((part) => part.trim());
            updateConstant({ value });
          }} placeholder={constant.type === "string-array" ? "значение 1, значение 2" : undefined} />}
        </label>
        <Button type="button" variant="outline" className="min-h-11 self-end text-destructive" aria-label={`Удалить публичное значение ${constant.label}`} disabled={users.length > 0} onClick={async () => {
          if (!(await confirm({ title: "Удалить публичное значение?", description: `Значение «${constant.label}» будет удалено из рабочей копии сайта.`, confirmLabel: "Удалить", destructive: true }))) return;
          onChange(constants.filter((item) => item !== constant));
        }}>Удалить</Button>
        {users.length > 0 && <p className="text-xs text-warning sm:col-span-5">Сначала перепривяжите источники: {users.join(", ")}.</p>}
      </fieldset>;
    })}
  </div>;
}

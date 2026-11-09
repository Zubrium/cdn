import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus, RotateCcw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/api";
import { useConfirm } from "@/components/common/confirm";
import { ErrorState, LoadingPage } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { BrandAssetEditor } from "@/domains/design-system/BrandAssetEditor";
import { compareWithParent } from "@/domains/design-system/parent-comparison";
import type { ParentTokenDifference } from "@/domains/design-system/parent-comparison";
import { TokenValueEditor } from "@/domains/design-system/TokenValueEditor";
import { baseTokenIsReferenced, designNameErrors, effectiveBaseValue, effectiveFeatureValue, initialTokenValue, kindNames } from "@/domains/design-system/types";
import type { BaseToken, StructuredDesignSystem, TokenBreakpoint, TokenKind, TokenValue } from "@/domains/design-system/types";
import { useSiteWorkspace } from "./SiteWorkspace";

const selectClass = "h-10 min-w-0 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring";

function ParentDifference({ difference }: { difference?: ParentTokenDifference }) {
  if (!difference) return null;
  if (difference.kind === "new") return <p className="mt-2 text-xs font-medium">Новый относительно родителя</p>;
  return <div className="mt-2 space-y-1 text-xs">
    <p className="font-medium">Изменено относительно родителя</p>
    <p className="[overflow-wrap:anywhere] text-muted-foreground">В родителе · {difference.previous}{difference.previousSource && ` · ${difference.previousSource}`}</p>
  </div>;
}

export default function DesignSystemPage() {
  const confirm = useConfirm();
  const { designSystem: theme, updateDesignSystem, snapshot, saving } = useSiteWorkspace();
  const snapshotId = snapshot?.id ?? "";
  const parentId = snapshot?.parentSnapshotId ?? "";
  const [tab, setTab] = useState("base");
  const [breakpointId, setBreakpointId] = useState("");
  const [search, setSearch] = useState("");
  const [groupName, setGroupName] = useState("");
  const [tokenName, setTokenName] = useState("");
  const [tokenGroup, setTokenGroup] = useState("");
  const [tokenKind, setTokenKind] = useState<TokenKind>("color");
  const [breakpointName, setBreakpointName] = useState("");
  const [breakpointWidth, setBreakpointWidth] = useState(768);
  useEffect(() => setBreakpointId(""), [snapshotId]);

  const workspace = useQuery({ queryKey: ["snapshot-design-system", snapshotId], queryFn: ({ signal }) => api.siteBuilder.designSnapshot(snapshotId, signal), enabled: Boolean(snapshotId) });
  const parent = useQuery({
    queryKey: ["snapshot-design-system", parentId],
    queryFn: ({ signal }) => api.siteBuilder.designSnapshot(parentId, signal),
    enabled: Boolean(parentId) && theme.formatVersion === 2,
    retry: false,
  });
  const nameErrors = useMemo(() => designNameErrors(theme), [theme]);
  const retained = useMemo(() => [
    ...(theme.legacyFeatureValues ?? []).map((item, index) => ({ item, index, breakpointId: "", location: "Общие настройки" })),
    ...theme.breakpoints.flatMap((bp) => (bp.legacyFeatureValues ?? []).map((item, index) => ({ item, index, breakpointId: bp.id, location: bp.name }))),
  ], [theme]);
  const breakpoint = theme.breakpoints.find((item) => item.id === breakpointId);
  const comparison = parent.data?.designSystem.formatVersion === 2 ? compareWithParent(theme, breakpoint, parent.data) : undefined;
  const featureTokens = useMemo(() => workspace.data?.featureTokens ?? [], [workspace.data?.featureTokens]);
  const featureGroups = useMemo(() => [...new Map(featureTokens.map((item) => [item.featureKey, item.featureName])).entries()], [featureTokens]);
  const fonts = theme.fonts;
  const update = (fn: (current: StructuredDesignSystem) => StructuredDesignSystem) => updateDesignSystem(fn);
  const updateBreakpoint = (fn: (current: TokenBreakpoint) => TokenBreakpoint) => update((current) => ({ ...current, breakpoints: current.breakpoints.map((item) => item.id === breakpointId ? fn(item) : item) }));

  const changeBase = (token: BaseToken, value: TokenValue) => {
    if (breakpoint) updateBreakpoint((current) => ({ ...current, baseValues: { ...current.baseValues, [token.id]: value } }));
    else update((current) => ({ ...current, baseTokens: current.baseTokens.map((item) => item.id === token.id ? { ...item, value } : item) }));
  };
  const changeFeature = (key: string, value?: TokenValue) => {
    const change = (values: Record<string, TokenValue>) => { const next = { ...values }; if (value) next[key] = value; else delete next[key]; return next; };
    if (breakpoint) updateBreakpoint((current) => ({ ...current, featureValues: change(current.featureValues) }));
    else update((current) => ({ ...current, featureValues: change(current.featureValues) }));
  };
  const addGroup = () => { if (!groupName.trim()) return; const id = crypto.randomUUID(); update((current) => ({ ...current, groups: [...current.groups, { id, name: groupName.trim() }] })); setGroupName(""); setTokenGroup(id); };
  const addToken = () => { const groupId = tokenGroup || theme?.groups[0]?.id; if (!tokenName.trim() || !groupId) return; update((current) => ({ ...current, baseTokens: [...current.baseTokens, { id: crypto.randomUUID(), name: tokenName.trim(), groupId, kind: tokenKind, value: initialTokenValue(tokenKind) }] })); setTokenName(""); };
  const renameGroup = (id: string, name: string) => update((current) => ({ ...current, groups: current.groups.map((item) => item.id === id ? { ...item, name } : item) }));
  const editBase = (id: string, patch: Partial<Pick<BaseToken, "name" | "groupId">>) => update((current) => ({ ...current, baseTokens: current.baseTokens.map((item) => item.id === id ? { ...item, ...patch } : item) }));
  const removeBase = async (token: BaseToken) => {
    if (!theme) return;
    if (baseTokenIsReferenced(theme, token.id)) { toast.error("Сначала снимите связи с этим токеном в настройках фич и размеров экрана."); return; }
    if (!(await confirm({ title: `Удалить «${token.name}»?`, description: "Токен и его переопределения будут удалены из новой редакции дизайна.", confirmLabel: "Удалить", destructive: true }))) return;
    update((current) => ({ ...current, baseTokens: current.baseTokens.filter((item) => item.id !== token.id), breakpoints: current.breakpoints.map((bp) => { const values = { ...bp.baseValues }; delete values[token.id]; return { ...bp, baseValues: values }; }) }));
  };
  const removeGroup = async (id: string) => {
    if (theme.baseTokens.some((item) => item.groupId === id)) { toast.error("Сначала удалите или переместите токены группы."); return; }
    if (!(await confirm({ title: "Удалить группу?", description: "Группа будет удалена из новой редакции дизайна.", confirmLabel: "Удалить", destructive: true }))) return;
    update((current) => ({ ...current, groups: current.groups.filter((item) => item.id !== id) }));
  };
  const addBreakpoint = () => {
    if (!breakpointName.trim() || !Number.isInteger(breakpointWidth) || breakpointWidth < 0) return;
    if (theme.breakpoints.some((item) => item.minWidthPx === breakpointWidth)) { toast.error("Такой порог ширины уже существует."); return; }
    const id = crypto.randomUUID(); update((current) => ({ ...current, breakpoints: [...current.breakpoints, { id, name: breakpointName.trim(), minWidthPx: breakpointWidth, baseValues: {}, featureValues: {} }].sort((a, b) => a.minWidthPx - b.minWidthPx) })); setBreakpointName(""); setBreakpointId(id);
  };
  const removeBreakpoint = async () => {
    if (!breakpoint) return;
    const count = Object.keys(breakpoint.baseValues).length + Object.keys(breakpoint.featureValues).length + (breakpoint.legacyFeatureValues?.length ?? 0);
    if (!(await confirm({ title: `Удалить размер «${breakpoint.name}»?`, description: `Будет удалено переопределений: ${count}. Остальные размеры и базовые значения сохранятся.`, confirmLabel: "Удалить размер", destructive: true }))) return;
    update((current) => ({ ...current, breakpoints: current.breakpoints.filter((item) => item.id !== breakpoint.id) })); setBreakpointId("");
  };

  const removeRetained = async (breakpointId: string, index: number) => {
    if (!(await confirm({ title: "Удалить сохранённую настройку?", description: "Значение больше нельзя будет восстановить при повторном подключении совместимой версии фичи.", confirmLabel: "Удалить", destructive: true }))) return;
    update((current) => breakpointId
      ? { ...current, breakpoints: current.breakpoints.map((bp) => bp.id === breakpointId ? { ...bp, legacyFeatureValues: (bp.legacyFeatureValues ?? []).filter((_, i) => i !== index) } : bp) }
      : { ...current, legacyFeatureValues: (current.legacyFeatureValues ?? []).filter((_, i) => i !== index) });
  };

  return <div className="page-grid min-w-0">
    <header className="flex flex-col gap-4 border-b border-border pb-4 lg:flex-row lg:items-end lg:justify-between">
      <div><h1 className="text-xl font-semibold tracking-tight">Дизайн-система</h1><p className="mt-1 max-w-2xl text-sm text-muted-foreground">Базовые настройки и токены подключённых фич. Изменения сохраняются общей кнопкой редактора.</p></div>
    </header>
    <div className="grid gap-3 sm:grid-cols-2">
      {theme && <div className="flex items-end gap-2"><div className="min-w-0 flex-1 space-y-1"><Label htmlFor="design-width">Размер экрана</Label><select id="design-width" value={breakpointId} onChange={(event) => setBreakpointId(event.target.value)} className={`${selectClass} w-full`}><option value="">Базовые значения</option>{theme.breakpoints.map((bp) => <option key={bp.id} value={bp.id}>{bp.name} · от {bp.minWidthPx} px</option>)}</select></div>{breakpoint && <Button variant="outline" size="icon" aria-label="Удалить размер экрана" onClick={() => void removeBreakpoint()}><Trash2 /></Button>}</div>}
    </div>
    {nameErrors.length > 0 && <div role="alert" className="rounded-lg border border-warning bg-warning/10 p-3 text-sm"><p className="font-medium">Проверьте названия настроек</p><ul className="mt-1 list-inside list-disc">{nameErrors.map((issue, index) => <li key={`${issue}-${index}`}>{issue}</li>)}</ul></div>}
    {retained.length > 0 && <details className="rounded-lg border border-warning bg-warning/10 p-3 text-sm"><summary className="cursor-pointer font-medium">Сохранены настройки, которые сейчас не применяются: {retained.length}</summary><p className="mt-2 text-muted-foreground">Фича удалена или тип её настройки изменился. Значения сохранены и восстановятся при подключении совместимой версии, если вы ещё не задали новое значение.</p><ul className="mt-2 divide-y divide-border">{retained.map(({ item, index, breakpointId, location }) => <li key={`${breakpointId}-${index}`} className="flex flex-wrap items-center justify-between gap-2 py-2"><span>{item.featureName} · {item.label} · {location}{item.version ? ` · версия ${item.version}` : ""}</span><Button variant="ghost" size="sm" onClick={() => void removeRetained(breakpointId, index)}>Удалить</Button></li>)}</ul></details>}
    {workspace.isLoading && <LoadingPage label="Загружаем дизайн снапшота…" />}
    {workspace.error && <ErrorState error={workspace.error} onRetry={() => void workspace.refetch()} />}
    {theme.formatVersion !== 2 && <p role="alert" className="rounded-lg border border-warning p-4 text-sm">В этом снапшоте сохранён прежний формат темы. Для новой модели создайте снапшот с графической дизайн-системой.</p>}
    {parentId && theme.formatVersion === 2 && tab !== "brand" && <>
      {parent.isLoading && <p role="status" className="text-sm text-muted-foreground">Загружаем значения родителя…</p>}
      {parent.isError && !parent.data && <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border p-3 text-sm">
        <p role="status">Не удалось загрузить значения родителя.</p>
        <Button variant="outline" size="sm" onClick={() => void parent.refetch()} disabled={parent.isFetching}>Повторить сравнение</Button>
      </div>}
      {parent.data && !comparison && <p className="text-sm text-muted-foreground">В родительском снапшоте сохранён прежний формат темы. Сравнение недоступно.</p>}
    </>}
    {theme.formatVersion === 2 && <fieldset disabled={saving} className="min-w-0 space-y-6"><legend className="sr-only">Настройки дизайна</legend>
      <details className="rounded-lg border border-border px-4 py-3"><summary className="cursor-pointer text-sm font-medium">Размеры экрана</summary><div className="mt-3 flex flex-wrap items-end gap-3"><div className="space-y-1"><Label htmlFor="breakpoint-name">Название</Label><Input id="breakpoint-name" value={breakpointName} onChange={(event) => setBreakpointName(event.target.value)} placeholder="Планшет" /></div><div className="space-y-1"><Label htmlFor="breakpoint-width">От ширины, px</Label><Input id="breakpoint-width" type="number" min="0" step="1" value={breakpointWidth} onChange={(event) => setBreakpointWidth(Number(event.target.value))} /></div><Button variant="outline" onClick={addBreakpoint} disabled={!breakpointName.trim()}><Plus />Добавить размер</Button></div><p className="mt-3 text-xs text-muted-foreground">Наибольший подходящий порог переопределяет предыдущие значения. Остальные настройки наследуются.</p></details>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"><nav aria-label="Разделы дизайн-системы" className="flex flex-wrap gap-1"><Button variant={tab === "base" ? "secondary" : "ghost"} onClick={() => setTab("base")}>Базовые токены</Button>{featureGroups.map(([key, name]) => <Button key={key} variant={tab === key ? "secondary" : "ghost"} onClick={() => setTab(key)}>{name}</Button>)}<Button variant={tab === "brand" ? "secondary" : "ghost"} onClick={() => setTab("brand")}>Шрифты и бренд</Button></nav><Input aria-label="Поиск токена" value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Найти настройку" className="sm:max-w-60" /></div>
      {tab === "base" && <section className="space-y-6" aria-label="Базовые токены">
        {!breakpoint && <details className="rounded-lg border border-border px-4 py-3"><summary className="cursor-pointer text-sm font-medium">Добавить группу или токен</summary><div className="mt-3 flex flex-wrap gap-2"><Input aria-label="Название группы" value={groupName} onChange={(event) => setGroupName(event.target.value)} placeholder="Название группы" className="sm:max-w-64" /><Button variant="outline" disabled={!groupName.trim()} onClick={addGroup}><Plus />Группа</Button></div><div className="mt-4 flex flex-wrap gap-2"><Input aria-label="Название токена" value={tokenName} onChange={(event) => setTokenName(event.target.value)} placeholder="Название настройки" className="sm:max-w-64" /><select aria-label="Группа токена" value={tokenGroup || theme.groups[0]?.id || ""} onChange={(event) => setTokenGroup(event.target.value)} className={selectClass}>{theme.groups.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}</select><select aria-label="Тип токена" value={tokenKind} onChange={(event) => setTokenKind(event.target.value as TokenKind)} className={selectClass}>{Object.entries(kindNames).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</select><Button variant="outline" disabled={!tokenName.trim() || theme.groups.length === 0} onClick={addToken}><Plus />Токен</Button></div></details>}
        {theme.groups.length === 0 && <p className="py-8 text-center text-sm text-muted-foreground">Создайте первую группу, затем добавьте настройки.</p>}
        {theme.groups.map((group) => <section key={group.id} aria-labelledby={`design-group-${group.id}`}><div className="flex items-center justify-between border-b border-border pb-2"><div className="min-w-0 flex-1"><h2 id={`design-group-${group.id}`} className="font-medium">{group.name}</h2>{!breakpoint && <details className="mt-1"><summary className="cursor-pointer text-xs text-muted-foreground">Изменить название</summary><Input aria-label="Название группы" value={group.name} onChange={(event) => renameGroup(group.id, event.target.value)} className="mt-2 max-w-sm" /></details>}</div>{!breakpoint && <Button variant="ghost" size="icon-sm" aria-label={`Удалить группу ${group.name}`} onClick={() => void removeGroup(group.id)}><Trash2 /></Button>}</div><div className="divide-y divide-border">{theme.baseTokens.filter((token) => token.groupId === group.id && token.name.toLocaleLowerCase().includes(search.toLocaleLowerCase())).map((token) => <div key={token.id} className="grid gap-3 py-4 lg:grid-cols-[minmax(10rem,1fr)_minmax(0,2fr)_auto]"><div><p className="text-sm font-medium">{token.name}</p>{!breakpoint && <details className="my-1"><summary className="cursor-pointer text-xs text-muted-foreground">Название и группа</summary><div className="mt-2 space-y-2"><Input aria-label="Название токена" value={token.name} onChange={(event) => editBase(token.id, { name: event.target.value })} /><select aria-label="Группа настройки" value={token.groupId} onChange={(event) => editBase(token.id, { groupId: event.target.value })} className={`${selectClass} w-full`}>{theme.groups.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></div></details>}<p className="text-xs text-muted-foreground">{kindNames[token.kind]}{breakpoint && !breakpoint.baseValues[token.id] ? " · наследуется" : ""}</p><ParentDifference difference={comparison?.base(token)} /></div><TokenValueEditor id={`base-${token.id}`} kind={token.kind} value={effectiveBaseValue(theme, token, breakpointId)} onChange={(value) => changeBase(token, value)} fonts={fonts} disabled={saving} />{breakpoint ? <Button variant="ghost" size="icon-sm" aria-label={`Сбросить ${token.name}`} disabled={!breakpoint.baseValues[token.id]} onClick={() => updateBreakpoint((bp) => { const values = { ...bp.baseValues }; delete values[token.id]; return { ...bp, baseValues: values }; })}><RotateCcw /></Button> : <Button variant="ghost" size="icon-sm" aria-label={`Удалить ${token.name}`} onClick={() => void removeBase(token)}><Trash2 /></Button>}</div>)}</div></section>)}
      </section>}
      {tab !== "base" && tab !== "brand" && <section className="divide-y divide-border" aria-label="Токены фичи">{featureTokens.filter((item) => item.featureKey === tab && `${item.definition.label} ${item.definition.group}`.toLocaleLowerCase().includes(search.toLocaleLowerCase())).map((item) => {
        const definition = item.definition; const key = `${item.featureKey}/${definition.key}`;
        const override = breakpoint ? breakpoint.featureValues[key] : theme.featureValues[key];
        const value = effectiveFeatureValue(theme, item, breakpointId);
        const compatible = theme.baseTokens.filter((base) => base.kind === definition.kind);
        return <div key={key} className="grid gap-3 py-4 lg:grid-cols-[minmax(10rem,1fr)_minmax(0,2fr)_auto]"><div><p className="text-sm font-medium">{definition.label}</p><p className="text-xs text-muted-foreground">{definition.group} · {override ? value.baseTokenId ? "Связан с базовым" : "Своё значение" : breakpoint ? "Наследуется" : "Из фичи"}</p><ParentDifference difference={comparison?.feature(item)} /></div><div className="space-y-3"><select aria-label={`Источник: ${definition.label}`} value={value.baseTokenId ?? "literal"} onChange={(event) => {
          if (event.target.value !== "literal") { changeFeature(key, { baseTokenId: event.target.value }); return; }
          const linked = theme.baseTokens.find((base) => base.id === value.baseTokenId);
          changeFeature(key, linked ? effectiveBaseValue(theme, linked, breakpointId) : definition.default);
        }} disabled={saving} className={`${selectClass} w-full`}><option value="literal">Своё значение</option>{compatible.map((base) => <option key={base.id} value={base.id}>Связать: {base.name}</option>)}</select>{!value.baseTokenId && <TokenValueEditor id={`feature-${item.featureKey}-${definition.key}`} kind={definition.kind} value={value} onChange={(next) => changeFeature(key, next)} fonts={fonts} disabled={saving} />}</div><Button variant="ghost" size="icon-sm" aria-label={`Сбросить ${definition.label}`} disabled={!override || saving} onClick={() => changeFeature(key)}><RotateCcw /></Button></div>;
      })}</section>}
      {tab === "brand" && <BrandAssetEditor theme={theme} update={update} />}
    </fieldset>}
  </div>;
}

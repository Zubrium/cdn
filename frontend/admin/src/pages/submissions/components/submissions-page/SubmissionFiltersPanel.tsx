import { RotateCcw, Search, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { SubmissionStatus } from "@/types";

const statusOptions: Array<{ value: SubmissionStatus | "all"; label: string }> = [
  { value: "all", label: "Все статусы" },
  { value: "new", label: "Новые" },
  { value: "in_progress", label: "В работе" },
  { value: "processed", label: "Обработанные" },
  { value: "rejected", label: "Отклонённые" },
];

type FilterUpdate = (values: Record<string, string | null>, replace?: boolean) => void;

type Props = {
  form: string;
  forms: Array<{ key: string; name: string }>;
  branchId: string;
  branches: Array<{ id: string; name: string; enabled: boolean }>;
  status: SubmissionStatus | "all";
  field: string;
  fields: Array<{ key: string; label: string }>;
  sort: string;
  searchInput: string;
  onSearchInputChange: (value: string) => void;
  from: string;
  to: string;
  loadedCount: number;
  hasNextPage: boolean;
  filtersActive: boolean;
  onUpdate: FilterUpdate;
  onReset: () => void;
};

export function SubmissionFiltersPanel({
  form, forms, branchId, branches, status, field, fields, sort,
  searchInput, onSearchInputChange, from, to, loadedCount, hasNextPage,
  filtersActive, onUpdate, onReset,
}: Props) {
  return (
    <section aria-label="Фильтры заявок" className="space-y-3">
      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-5">
        <div className="space-y-1.5">
          <Label htmlFor="filter-form">Форма</Label>
          <Select value={form || "none"} onValueChange={(value) => onUpdate({ form: value === "none" ? null : value, field: null })}>
            <SelectTrigger id="filter-form" className="w-full"><SelectValue placeholder="Выберите форму" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="none">Все формы</SelectItem>
              {forms.map((item) => <SelectItem key={item.key} value={item.key}>{item.name}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="filter-branch">Ветка</Label>
          <Select value={branchId} onValueChange={(value) => onUpdate({ branchId: value, environment: null, field: null })}>
            <SelectTrigger id="filter-branch" className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">Все ветки</SelectItem>
              {branches.map((branch) => <SelectItem key={branch.id} value={branch.id}>{branch.name}{branch.enabled ? "" : " · отключена"}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="filter-status">Статус</Label>
          <Select value={status} onValueChange={(value) => onUpdate({ status: value })}>
            <SelectTrigger id="filter-status" className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>{statusOptions.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent>
          </Select>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="filter-field">Поле поиска</Label>
          <Select value={field || "all"} onValueChange={(value) => onUpdate({ field: value === "all" ? null : value })}>
            <SelectTrigger id="filter-field" className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">Любое поле</SelectItem>
              {fields.map((item) => <SelectItem key={item.key} value={item.key}>{item.label || item.key}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="filter-sort">Сортировка</Label>
          <Select value={sort} onValueChange={(value) => onUpdate({ sort: value })}>
            <SelectTrigger id="filter-sort" className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="desc">Сначала новые</SelectItem>
              <SelectItem value="asc">Сначала старые</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className="flex flex-col gap-2 sm:flex-row">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input type="search" value={searchInput} onChange={(event) => onSearchInputChange(event.target.value)} placeholder="Поиск по содержимому заявки" aria-label="Поиск заявок" className="pl-9 pr-9" />
          {searchInput && <button type="button" className="absolute right-2 top-1/2 -translate-y-1/2 rounded-sm p-1 text-muted-foreground hover:bg-accent" onClick={() => onSearchInputChange("")} aria-label="Очистить поиск"><X className="h-3.5 w-3.5" /></button>}
        </div>
        <div className="grid grid-cols-2 gap-2 sm:flex">
          <div className="space-y-1.5">
            <Label htmlFor="filter-from" className="text-2xs text-muted-foreground">С</Label>
            <Input id="filter-from" type="date" value={from} max={to || undefined} onChange={(event) => onUpdate({ from: event.target.value || null })} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="filter-to" className="text-2xs text-muted-foreground">По</Label>
            <Input id="filter-to" type="date" value={to} min={from || undefined} onChange={(event) => onUpdate({ to: event.target.value || null })} />
          </div>
        </div>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <span>Загружено: {loadedCount}{hasNextPage ? " (есть ещё)" : ""}</span>
        {filtersActive && <Button variant="ghost" size="sm" onClick={onReset}><RotateCcw />Сбросить фильтры</Button>}
      </div>
    </section>
  );
}

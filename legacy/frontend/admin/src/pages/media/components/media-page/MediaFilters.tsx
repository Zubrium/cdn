import { Search, X } from "lucide-react";
import type { MediaSort, MediaStatus } from "@/types";
import { InlineAlert } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { MediaGalleryParamUpdates } from "@/domains/media/gallery-filters";

const sortOptions: Array<{ value: MediaSort; label: string }> = [
  { value: "new", label: "Сначала новые" },
  { value: "old", label: "Сначала старые" },
  { value: "name", label: "По имени" },
  { value: "size_desc", label: "По размеру: больше" },
  { value: "size_asc", label: "По размеру: меньше" },
];

const typeOptions: Array<{ value: "all" | "image" | "video" | "pdf"; label: string }> = [
  { value: "all", label: "Все типы" },
  { value: "image", label: "Изображения" },
  { value: "video", label: "Видео" },
  { value: "pdf", label: "PDF" },
];

const statusOptions: Array<{ value: MediaStatus | "all"; label: string }> = [
  { value: "all", label: "Все статусы" },
  { value: "ready", label: "Готово" },
  { value: "processing", label: "Обработка" },
  { value: "failed", label: "Ошибка" },
];

type Props = {
  searchInput: string;
  onSearchInputChange: (value: string) => void;
  type: "all" | "image" | "video" | "pdf";
  status: MediaStatus | "all";
  sort: MediaSort;
  from: string;
  to: string;
  min: string;
  max: string;
  filtersActive: boolean;
  itemCount: number;
  hasNextPage: boolean;
  rangeError: string | null;
  onUpdate: (values: MediaGalleryParamUpdates, replace?: boolean) => void;
  onReset: () => void;
};

export function MediaFilters({
  searchInput,
  onSearchInputChange,
  type,
  status,
  sort,
  from,
  to,
  min,
  max,
  filtersActive,
  itemCount,
  hasNextPage,
  rangeError,
  onUpdate,
  onReset,
}: Props) {
  return (
    <section aria-label="Фильтры файлов" className="mb-4 space-y-3">
      <div className="flex flex-col gap-2 sm:flex-row">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            value={searchInput}
            onChange={(event) => onSearchInputChange(event.target.value)}
            placeholder="Поиск по имени файла"
            aria-label="Поиск медиа"
            className="pl-9 pr-9"
          />
          {searchInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 rounded-sm p-1 text-muted-foreground hover:bg-accent"
              onClick={() => onSearchInputChange("")}
              aria-label="Очистить поиск"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        <div className="grid grid-cols-2 gap-2 sm:flex">
          <Select value={type} onValueChange={(value) => onUpdate({ type: value })}>
            <SelectTrigger className="w-full sm:w-40" aria-label="Тип файла"><SelectValue /></SelectTrigger>
            <SelectContent>{typeOptions.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent>
          </Select>
          <Select value={status} onValueChange={(value) => onUpdate({ status: value })}>
            <SelectTrigger className="w-full sm:w-40" aria-label="Статус файла"><SelectValue /></SelectTrigger>
            <SelectContent>{statusOptions.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent>
          </Select>
          <Select value={sort} onValueChange={(value) => onUpdate({ sort: value })}>
            <SelectTrigger className="col-span-2 w-full sm:w-44" aria-label="Сортировка"><SelectValue /></SelectTrigger>
            <SelectContent>{sortOptions.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent>
          </Select>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <div><label htmlFor="media-from" className="mb-1 block text-2xs font-medium text-muted-foreground">Загружен с</label><Input id="media-from" type="date" value={from} onChange={(event) => onUpdate({ from: event.target.value || null })} aria-label="Дата загрузки с" /></div>
        <div><label htmlFor="media-to" className="mb-1 block text-2xs font-medium text-muted-foreground">Загружен по</label><Input id="media-to" type="date" value={to} onChange={(event) => onUpdate({ to: event.target.value || null })} aria-label="Дата загрузки по" /></div>
        <div><label htmlFor="media-min" className="mb-1 block text-2xs font-medium text-muted-foreground">Размер от, МБ</label><Input id="media-min" type="number" min={0} step="0.1" value={min} onChange={(event) => onUpdate({ min: event.target.value || null })} aria-label="Минимальный размер в мегабайтах" /></div>
        <div><label htmlFor="media-max" className="mb-1 block text-2xs font-medium text-muted-foreground">Размер до, МБ</label><Input id="media-max" type="number" min={0} step="0.1" value={max} onChange={(event) => onUpdate({ max: event.target.value || null })} aria-label="Максимальный размер в мегабайтах" /></div>
      </div>

      {rangeError && <InlineAlert tone="warning">{rangeError}</InlineAlert>}
      <div className="flex flex-wrap items-center gap-2">
        {filtersActive && <Button variant="ghost" size="sm" onClick={onReset}><X />Сбросить фильтры</Button>}
        <Badge variant="muted">Загружено: {itemCount}{hasNextPage ? "+" : ""}</Badge>
        {filtersActive && <span className="text-2xs text-muted-foreground">Поиск, тип, даты, размер и статус применяются на сервере.</span>}
      </div>
    </section>
  );
}

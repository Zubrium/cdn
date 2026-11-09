import { RefreshCw, Search, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { StatusFilter } from "@/pages/access/components/tokens/token-utils";

type TokensToolbarProps = {
  search: string;
  status: StatusFilter;
  isRefreshing: boolean;
  onSearchChange: (value: string) => void;
  onStatusChange: (value: StatusFilter) => void;
  onRefresh: () => void;
};

export function TokensToolbar({
  search,
  status,
  isRefreshing,
  onSearchChange,
  onStatusChange,
  onRefresh,
}: TokensToolbarProps) {
  return (
    <section aria-label="Поиск и фильтры токенов" className="flex flex-col gap-2 sm:flex-row">
      <div className="relative flex-1">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
        <Input
          type="search"
          value={search}
          onChange={(event) => onSearchChange(event.target.value)}
          placeholder="Поиск по названию, роли или ID"
          aria-label="Поиск токенов"
          className="pl-9 pr-9"
        />
        {search && (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="absolute right-1 top-1/2 h-8 w-8 -translate-y-1/2"
            onClick={() => onSearchChange("")}
            aria-label="Очистить поиск"
          >
            <X className="h-3.5 w-3.5" aria-hidden="true" />
          </Button>
        )}
      </div>
      <Select value={status} onValueChange={(value) => onStatusChange(value as StatusFilter)}>
        <SelectTrigger className="w-full sm:w-44" aria-label="Статус токена">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="all">Все статусы</SelectItem>
          <SelectItem value="active">Активные</SelectItem>
          <SelectItem value="revoked">Отозванные</SelectItem>
          <SelectItem value="expired">Истёкшие</SelectItem>
        </SelectContent>
      </Select>
      <Button
        type="button"
        variant="outline"
        onClick={onRefresh}
        loading={isRefreshing}
      >
        <RefreshCw aria-hidden="true" />
        Обновить
      </Button>
    </section>
  );
}

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { KeyRound, RefreshCw, Search, UsersRound, X } from "lucide-react";
import { api } from "@/api";
import type { Role } from "@/types";
import { queryKeys } from "@/lib/query-keys";
import { formatDate, formatDateTime, roleLabels } from "@/lib/format";
import {
  EmptyState,
  ErrorState,
  TableSkeleton,
} from "@/components/common/page";
import { RoleBadge, TokenStatusBadge } from "@/components/common/status-badges";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

type RoleFilter = Role | "all";

export default function UsersPage() {
  const [search, setSearch] = useState("");
  const [role, setRole] = useState<RoleFilter>("all");

  const tokensQuery = useQuery({
    queryKey: queryKeys.tokens.list(),
    queryFn: ({ signal }) => api.tokens.list(signal),
  });

  const all = useMemo(() => tokensQuery.data?.items ?? [], [tokensQuery.data]);

  const users = useMemo(() => {
    const query = search.trim().toLowerCase();
    return all.filter((token) => {
      if (role !== "all" && token.role !== role) return false;
      if (
        query &&
        !`${token.name} ${roleLabels[token.role]} ${token.id}`
          .toLowerCase()
          .includes(query)
      ) {
        return false;
      }
      return true;
    });
  }, [all, role, search]);

  const counts = useMemo(
    () => ({
      admin: all.filter((token) => token.role === "admin" && !token.revoked_at).length,
      moderator: all.filter(
        (token) => token.role === "moderator" && !token.revoked_at,
      ).length,
      user: all.filter((token) => token.role === "user" && !token.revoked_at).length,
    }),
    [all],
  );

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h2 className="text-lg font-semibold tracking-tight">Пользователи</h2>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            onClick={() => void tokensQuery.refetch()}
            loading={tokensQuery.isFetching}
          >
            <RefreshCw />
            Обновить
          </Button>
          <Button asChild>
            <Link to="/access/tokens">
              <KeyRound />
              Управлять токенами
            </Link>
          </Button>
        </div>
      </div>

      <div className="grid grid-cols-3 gap-2">
        <div className="rounded-lg border border-border bg-card p-3">
          <p className="text-2xs text-muted-foreground">Администраторы</p>
          <p className="mt-0.5 text-lg font-semibold">{counts.admin}</p>
        </div>
        <div className="rounded-lg border border-border bg-card p-3">
          <p className="text-2xs text-muted-foreground">Модераторы</p>
          <p className="mt-0.5 text-lg font-semibold">{counts.moderator}</p>
        </div>
        <div className="rounded-lg border border-border bg-card p-3">
          <p className="text-2xs text-muted-foreground">Пользователи</p>
          <p className="mt-0.5 text-lg font-semibold">{counts.user}</p>
        </div>
      </div>

      <div className="flex flex-col gap-2 sm:flex-row">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Поиск по имени, роли или ID"
            aria-label="Поиск пользователей"
            className="pl-9 pr-9"
          />
          {search && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 rounded-sm p-1 text-muted-foreground hover:bg-accent"
              onClick={() => setSearch("")}
              aria-label="Очистить поиск"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        <Select value={role} onValueChange={(value) => setRole(value as RoleFilter)}>
          <SelectTrigger className="w-full sm:w-44" aria-label="Роль пользователя">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">Все роли</SelectItem>
            <SelectItem value="admin">{roleLabels.admin}</SelectItem>
            <SelectItem value="moderator">{roleLabels.moderator}</SelectItem>
            <SelectItem value="user">{roleLabels.user}</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {tokensQuery.isLoading ? (
        <TableSkeleton rows={5} />
      ) : tokensQuery.error ? (
        <ErrorState
          error={tokensQuery.error}
          onRetry={() => void tokensQuery.refetch()}
        />
      ) : users.length === 0 ? (
        <EmptyState
          icon={UsersRound}
          title={search || role !== "all" ? "Пользователи не найдены" : "Пользователей пока нет"}
          description={
            search || role !== "all"
              ? "Измените запрос или фильтр роли."
              : "Выпустите первый токен, чтобы добавить пользователя."
          }
          action={
            <Button asChild>
              <Link to="/access/tokens">
                <KeyRound />
                Создать токен
              </Link>
            </Button>
          }
        />
      ) : (
        <>
        <div className="table-wrap hidden md:block">
          <table className="data-table min-w-[760px]">
            <thead>
              <tr>
                <th>Имя</th>
                <th>Роль</th>
                <th>Статус</th>
                <th>Создан</th>
                <th>Действует до</th>
                <th>Последний вход</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {users.map((user) => (
                <tr key={user.id}>
                  <td>
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium">{user.name}</p>
                      <code className="block truncate text-2xs text-muted-foreground">
                        {user.id}
                      </code>
                    </div>
                  </td>
                  <td>
                    <RoleBadge role={user.role} />
                  </td>
                  <td>
                    <TokenStatusBadge token={user} />
                  </td>
                  <td className="text-xs text-muted-foreground">
                    {formatDateTime(user.created_at)}
                  </td>
                  <td className="text-xs text-muted-foreground">
                    {user.expires_at ? formatDate(user.expires_at) : "Без срока"}
                  </td>
                  <td className="text-xs text-muted-foreground">
                    {user.last_used_at
                      ? formatDateTime(user.last_used_at)
                      : "Не использовался"}
                  </td>
                  <td>
                    <div className="flex justify-end">
                      <Button variant="ghost" size="sm" asChild>
                        <Link
                          to={`/access/tokens?q=${encodeURIComponent(user.name)}`}
                        >
                          <KeyRound />
                          Токен
                        </Link>
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="space-y-3 md:hidden">
          {users.map((user) => (
            <div
              key={user.id}
              className="rounded-lg border border-border bg-card p-3"
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium">{user.name}</p>
                  <code className="block truncate text-2xs text-muted-foreground">
                    {user.id}
                  </code>
                </div>
                <RoleBadge role={user.role} />
              </div>
              <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <TokenStatusBadge token={user} />
                <span>Создан {formatDateTime(user.created_at)}</span>
                <span>
                  До {user.expires_at ? formatDate(user.expires_at) : "бессрочно"}
                </span>
              </div>
              <div className="mt-3 flex justify-end">
                <Button variant="ghost" size="sm" asChild>
                  <Link to={`/access/tokens?q=${encodeURIComponent(user.name)}`}>
                    <KeyRound />
                    Токен
                  </Link>
                </Button>
              </div>
            </div>
          ))}
        </div>
        </>
      )}
    </div>
  );
}

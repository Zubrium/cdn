import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/api";
import type { CreatedToken, Token } from "@/types";
import { useAuth } from "@/app/auth";
import { queryKeys } from "@/lib/query-keys";
import { errorMessage } from "@/lib/errors";
import { roleLabels } from "@/lib/format";
import { EmptyState, ErrorState, InlineAlert, TableSkeleton } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { Button } from "@/components/ui/button";
import { CreateTokenDialog } from "@/pages/access/components/tokens/CreateTokenDialog";
import { CreatedTokenDialog } from "@/pages/access/components/tokens/CreatedTokenDialog";
import { TokensList } from "@/pages/access/components/tokens/TokensList";
import { TokensSummary } from "@/pages/access/components/tokens/TokensSummary";
import { TokensToolbar } from "@/pages/access/components/tokens/TokensToolbar";
import { tokenStatus, type StatusFilter } from "@/pages/access/components/tokens/token-utils";

export default function TokensPage() {
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const { principal } = useAuth();
  const [searchParams, setSearchParams] = useSearchParams();
  const urlQuery = searchParams.get("q") ?? "";
  const [search, setSearch] = useState(urlQuery);
  const [status, setStatus] = useState<StatusFilter>(
    (searchParams.get("status") as StatusFilter | null) ?? "all",
  );
  const [createOpen, setCreateOpen] = useState(false);
  const [created, setCreated] = useState<CreatedToken | null>(null);
  const [createdOpen, setCreatedOpen] = useState(false);

  useEffect(() => {
    setSearch(urlQuery);
  }, [urlQuery]);

  const updateParams = (values: Record<string, string | null>) => {
    setSearchParams(
      (current) => {
        const next = new URLSearchParams(current);
        Object.entries(values).forEach(([key, value]) => {
          if (value === null || value === "" || value === "all") {
            next.delete(key);
          } else {
            next.set(key, value);
          }
        });
        return next;
      },
      { replace: true },
    );
  };

  const tokensQuery = useQuery({
    queryKey: queryKeys.tokens.list(),
    queryFn: ({ signal }) => api.tokens.list(signal),
  });

  const revokeMutation = useMutation({
    mutationFn: (id: string) => api.tokens.revoke(id),
    onSuccess: () => {
      toast.success("Токен отозван");
      void queryClient.invalidateQueries({ queryKey: queryKeys.tokens.all });
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const all = useMemo(() => tokensQuery.data?.items ?? [], [tokensQuery.data?.items]);
  const tokens = useMemo(() => {
    const query = search.trim().toLowerCase();
    return all.filter((token) => {
      if (status !== "all" && tokenStatus(token) !== status) return false;
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
  }, [all, search, status]);

  const activeCount = all.filter((token) => tokenStatus(token) === "active").length;
  const revokedCount = all.filter((token) => tokenStatus(token) === "revoked").length;

  const requestRevoke = async (token: Token) => {
    const self = principal?.id === token.id;
    const confirmed = await confirm({
      title: "Отозвать токен?",
      description: (
        <div className="space-y-2">
          <p>
            Токен <strong>«{token.name}»</strong> перестанет работать сразу.
          </p>
          {self && (
            <p className="font-medium text-destructive">
              Это токен текущей сессии — после отзыва вы выйдете из панели.
            </p>
          )}
          <p className="text-muted-foreground">
            Выпустить его заново нельзя, можно создать новый токен.
          </p>
        </div>
      ),
      confirmLabel: "Отозвать",
      destructive: true,
    });
    if (confirmed) revokeMutation.mutate(token.id);
  };

  const handleCreated = (token: CreatedToken) => {
    setCreated(token);
    setCreatedOpen(true);
    void queryClient.invalidateQueries({ queryKey: queryKeys.tokens.all });
  };

  const isFiltered = search.trim().length > 0 || status !== "all";

  return (
    <div className="space-y-4">
      <header className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h2 className="text-lg font-semibold tracking-tight">Токены доступа</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {principal?.role === "moderator"
              ? "Модератор видит и отзывает только выпущенные им токены."
              : "Токен задаёт роль учётной записи. Отзыв сразу прекращает доступ."}
          </p>
        </div>
        <Button type="button" onClick={() => setCreateOpen(true)}>
          <Plus aria-hidden="true" />
          Создать токен
        </Button>
      </header>

      <TokensSummary total={all.length} active={activeCount} revoked={revokedCount} />

      <TokensToolbar
        search={search}
        status={status}
        isRefreshing={tokensQuery.isFetching}
        onSearchChange={(value) => {
          setSearch(value);
          updateParams({ q: value || null });
        }}
        onStatusChange={(value) => {
          setStatus(value);
          updateParams({ status: value });
        }}
        onRefresh={() => void tokensQuery.refetch()}
      />

      {tokensQuery.isLoading ? (
        <TableSkeleton rows={5} />
      ) : tokensQuery.error ? (
        <ErrorState error={tokensQuery.error} onRetry={() => void tokensQuery.refetch()} />
      ) : tokens.length === 0 ? (
        <EmptyState
          icon={KeyRound}
          title={isFiltered ? "Токены не найдены" : "Токенов пока нет"}
          description={
            isFiltered
              ? "Измените запрос или фильтр статуса."
              : "Создайте первый токен, чтобы выдать доступ редактору или модератору."
          }
          action={
            <Button type="button" onClick={() => setCreateOpen(true)}>
              <Plus aria-hidden="true" />
              Создать токен
            </Button>
          }
        />
      ) : (
        <TokensList
          tokens={tokens}
          isRevoking={revokeMutation.isPending}
          onRevoke={(token) => void requestRevoke(token)}
        />
      )}

      <InlineAlert tone="info" title="Безопасность">
        Токены передаются в заголовке <code>Authorization: Bearer …</code>.
        Панель хранит только хеш секрета, поэтому восстановить забытый токен
        невозможно — его нужно отозвать и выпустить новый.
      </InlineAlert>

      <CreateTokenDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={handleCreated}
      />
      <CreatedTokenDialog
        token={created}
        open={createdOpen}
        onClose={() => setCreatedOpen(false)}
      />
    </div>
  );
}

import { Trash2 } from "lucide-react";
import { formatDate, formatDateTime } from "@/lib/format";
import type { Token } from "@/types";
import { RoleBadge, TokenStatusBadge } from "@/components/common/status-badges";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

type TokensListProps = {
  tokens: Token[];
  isRevoking: boolean;
  onRevoke: (token: Token) => void;
};

function RevokeAction({ token, disabled, onRevoke }: {
  token: Token;
  disabled: boolean;
  onRevoke: (token: Token) => void;
}) {
  if (token.revoked_at) return <Badge variant="muted">Отозван</Badge>;

  return (
    <Button
      variant="ghost"
      size="sm"
      className="text-destructive hover:text-destructive"
      onClick={() => onRevoke(token)}
      disabled={disabled}
    >
      <Trash2 aria-hidden="true" />
      Отозвать
    </Button>
  );
}

export function TokensList({ tokens, isRevoking, onRevoke }: TokensListProps) {
  return (
    <section aria-label="Список токенов">
      <div className="table-wrap hidden md:block">
        <table className="data-table min-w-[760px]">
          <caption className="sr-only">Токены доступа и их статусы</caption>
          <thead>
            <tr>
              <th scope="col">Название</th>
              <th scope="col">Роль</th>
              <th scope="col">Статус</th>
              <th scope="col">Создан</th>
              <th scope="col">Действует до</th>
              <th scope="col">Последнее использование</th>
              <th scope="col"><span className="sr-only">Действие</span></th>
            </tr>
          </thead>
          <tbody>
            {tokens.map((token) => (
              <tr key={token.id} className="group">
                <td>
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium">{token.name}</p>
                    <code className="block truncate text-2xs text-muted-foreground">{token.id}</code>
                  </div>
                </td>
                <td><RoleBadge role={token.role} /></td>
                <td><TokenStatusBadge token={token} /></td>
                <td className="text-xs text-muted-foreground">{formatDateTime(token.created_at)}</td>
                <td className="text-xs text-muted-foreground">{token.expires_at ? formatDate(token.expires_at) : "Без срока"}</td>
                <td className="text-xs text-muted-foreground">
                  {token.last_used_at ? formatDateTime(token.last_used_at) : "Не использовался"}
                </td>
                <td><div className="flex justify-end"><RevokeAction token={token} disabled={isRevoking} onRevoke={onRevoke} /></div></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <ul className="space-y-3 md:hidden" aria-label="Токены доступа">
        {tokens.map((token) => (
          <li key={token.id} className="rounded-lg border border-border bg-card p-3">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="truncate text-sm font-medium">{token.name}</p>
                <code className="block truncate text-2xs text-muted-foreground">{token.id}</code>
              </div>
              <RoleBadge role={token.role} />
            </div>
            <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
              <TokenStatusBadge token={token} />
              <span>Создан {formatDateTime(token.created_at)}</span>
              <span>До {token.expires_at ? formatDate(token.expires_at) : "бессрочно"}</span>
            </div>
            <div className="mt-3 flex justify-end">
              <RevokeAction token={token} disabled={isRevoking} onRevoke={onRevoke} />
            </div>
          </li>
        ))}
      </ul>
    </section>
  );
}

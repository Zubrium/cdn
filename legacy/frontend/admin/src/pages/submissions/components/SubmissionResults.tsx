import { ChevronDown } from "lucide-react";
import { Link } from "react-router-dom";
import type { Submission } from "@/types";
import { formatDateTime } from "@/lib/format";
import { SubmissionStatusBadge } from "@/components/common/status-badges";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { submissionDetailHref } from "../filters";

function summarize(data: Submission["data"]): string {
  const parts: string[] = [];
  for (const value of Object.values(data)) {
    if (typeof value === "string" && value.trim()) {
      parts.push(value.trim());
    } else if (typeof value === "number" || typeof value === "boolean") {
      parts.push(String(value));
    }
    if (parts.length >= 3) break;
  }
  return parts.join(" · ");
}

export function SubmissionResults({
  items,
  branchName,
  locationSearch,
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
}: {
  items: Submission[];
  branchName: (id: string) => string;
  locationSearch: string;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  onLoadMore: () => void;
}) {
  return (
    <>
      <section aria-label="Список заявок">
        <div className="hidden overflow-hidden rounded-lg border border-border bg-card md:block">
          <table className="data-table">
            <thead>
              <tr>
                <th scope="col">Получена</th>
                <th scope="col">Форма</th>
                <th scope="col">Содержание</th>
                <th scope="col">Статус</th>
                <th scope="col" className="text-right">Действия</th>
              </tr>
            </thead>
            <tbody>
              {items.map((submission) => (
                <tr key={submission.id}>
                  <td className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(submission.created_at)}</td>
                  <td className="text-xs">
                    <code className="text-2xs">{submission.feature_key}</code>
                    <Badge variant="secondary" className="ml-1">
                      {branchName(submission.branchId ?? submission.environment)}
                    </Badge>
                  </td>
                  <td className="max-w-md"><p className="truncate text-sm">{summarize(submission.data) || "—"}</p></td>
                  <td><SubmissionStatusBadge status={submission.status} /></td>
                  <td>
                    <div className="flex justify-end">
                      <Button variant="ghost" size="sm" asChild>
                        <Link to={submissionDetailHref(submission.id, locationSearch)}>Открыть</Link>
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className="grid gap-3 md:hidden">
          {items.map((submission) => (
            <Link
              key={submission.id}
              to={submissionDetailHref(submission.id, locationSearch)}
              className="rounded-lg border border-border bg-card p-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <div className="flex items-start justify-between gap-2">
                <p className="line-clamp-2 text-sm font-medium">{summarize(submission.data) || "Без данных"}</p>
                <SubmissionStatusBadge status={submission.status} />
              </div>
              <p className="mt-2 text-2xs text-muted-foreground">
                {submission.feature_key} · {branchName(submission.branchId ?? submission.environment)} · {formatDateTime(submission.created_at)}
              </p>
            </Link>
          ))}
        </div>
      </section>

      <div className="flex flex-col items-center justify-center gap-2">
        {hasNextPage ? (
          <Button
            variant="outline"
            loading={isFetchingNextPage}
            onClick={onLoadMore}
          >
            <ChevronDown />
            Загрузить ещё
          </Button>
        ) : (
          <p className="text-2xs text-muted-foreground">Все загруженные заявки показаны</p>
        )}
      </div>
    </>
  );
}

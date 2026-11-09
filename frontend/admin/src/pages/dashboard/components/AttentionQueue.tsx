import { AlertTriangle, CheckCircle2, CircleDashed, Inbox } from "lucide-react";
import { Link } from "react-router-dom";
import type { Overview } from "@/types";
import { formatDateTime } from "@/lib/format";
import { SubmissionStatusBadge } from "@/components/common/status-badges";
import { Badge } from "@/components/ui/badge";

type Attention = Overview["attention"];

export function AttentionQueue({ attention }: { attention: Attention | undefined }) {
  const total = attention
    ? attention.draft_model_snapshots.length + attention.new_submissions.length + attention.failed_media.length
    : 0;

  return (
    <section className="min-w-0 rounded-lg border border-border bg-card" aria-labelledby="attention-title">
      <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3 sm:px-5">
        <div>
          <h2 id="attention-title" className="text-sm font-semibold">Требует внимания</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">Незавершённые или проблемные операции</p>
        </div>
        <Badge variant={total > 0 ? "warning" : "success"}>
          {total > 0 ? total : "Нет задач"}
        </Badge>
      </div>
      <div className="divide-y divide-border">
        {total === 0 && (
          <div className="flex items-center gap-3 px-4 py-6 sm:px-5">
            <CheckCircle2 className="h-5 w-5 text-success" aria-hidden="true" />
            <div>
              <p className="text-sm font-medium">Всё под контролем</p>
              <p className="mt-0.5 text-xs text-muted-foreground">
                Нет черновиков без публикации, новых заявок или ошибок медиа.
              </p>
            </div>
          </div>
        )}

        {attention?.draft_model_snapshots.map((snapshot) => (
          <Link
            key={`draft-${snapshot.snapshot_id}`}
            to={`/content?snapshot=${encodeURIComponent(snapshot.snapshot_id)}`}
            className="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-muted/40 sm:px-5"
          >
            <CircleDashed className="mt-0.5 h-4 w-4 shrink-0 text-warning" aria-hidden="true" />
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">{snapshot.name}</p>
              <p className="mt-0.5 text-xs text-muted-foreground">
                Черновик модели · ревизия {snapshot.revision} · {formatDateTime(snapshot.updated_at)}
              </p>
            </div>
            <div className="hidden sm:block"><Badge variant="warning">Черновик данных</Badge></div>
          </Link>
        ))}

        {attention?.new_submissions.map((submission) => (
          <Link
            key={`submission-${submission.id}`}
            to={`/submissions/${submission.id}?status=new`}
            className="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-muted/40 sm:px-5"
          >
            <Inbox className="mt-0.5 h-4 w-4 shrink-0 text-info" aria-hidden="true" />
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">{submission.data_preview || "Новая заявка"}</p>
              <p className="mt-0.5 text-xs text-muted-foreground">
                <code>{submission.feature_key}</code> · {formatDateTime(submission.created_at)}
              </p>
            </div>
            <SubmissionStatusBadge status={submission.status} />
          </Link>
        ))}

        {attention?.failed_media.map((media) => (
          <Link
            key={`media-${media.id}`}
            to="/media?status=failed"
            className="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-muted/40 sm:px-5"
          >
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" aria-hidden="true" />
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">{media.original_filename}</p>
              <p className="mt-0.5 text-xs text-muted-foreground">Ошибка обработки · {formatDateTime(media.created_at)}</p>
            </div>
            <Badge variant="destructive">failed</Badge>
          </Link>
        ))}
      </div>
    </section>
  );
}

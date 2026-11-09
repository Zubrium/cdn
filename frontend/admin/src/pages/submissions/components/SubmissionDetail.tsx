import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, ArrowLeft, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/api";
import { useAuth } from "@/app/auth";
import type { JsonValue, SubmissionStatus } from "@/types";
import { queryKeys } from "@/lib/query-keys";
import { errorMessage } from "@/lib/errors";
import { canManageSubmissions } from "@/lib/permissions";
import { formatDateTime } from "@/lib/format";
import { ErrorState, InlineAlert } from "@/components/common/page";
import { SubmissionStatusBadge } from "@/components/common/status-badges";
import { useConfirm } from "@/components/common/confirm";
import { parsedSchema } from "@/domains/content-schema/schema";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/misc";

const nextStatuses: Record<SubmissionStatus, SubmissionStatus[]> = {
  new: ["in_progress", "processed", "rejected"],
  in_progress: ["processed", "rejected"],
  processed: ["in_progress"],
  rejected: ["in_progress", "processed"],
};

function valueToText(value: JsonValue | undefined): string {
  if (value === undefined || value === null) return "—";
  if (typeof value === "string") return value.trim() || "—";
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return JSON.stringify(value, null, 2);
}

export function SubmissionDetail({
  submissionId,
  onBack,
  onChanged,
}: {
  submissionId: string;
  onBack: () => void;
  onChanged: () => void;
}) {
  const { principal } = useAuth();
  const canDelete = principal?.role === "admin";
  const canManage = canManageSubmissions(principal?.role);
  const queryClient = useQueryClient();
  const confirm = useConfirm();

  const submissionQuery = useQuery({
    queryKey: queryKeys.submissions.detail(submissionId),
    queryFn: ({ signal }) => api.forms.get(submissionId, signal),
  });
  const submission = submissionQuery.data;

  const versionsQuery = useQuery({
    queryKey: queryKeys.features.versions(submission?.feature_id ?? ""),
    queryFn: ({ signal }) => api.features.versions(submission!.feature_id, signal),
    enabled: Boolean(submission),
  });

  const schemaVersion = useMemo(
    () =>
      versionsQuery.data?.items.find(
        (version) => version.id === submission?.schema_version_id,
      ) ?? null,
    [versionsQuery.data, submission],
  );

  const fields = useMemo(
    () => parsedSchema(schemaVersion?.schema).fields,
    [schemaVersion],
  );

  const statusMutation = useMutation({
    mutationFn: (next: SubmissionStatus) =>
      api.forms.updateStatus(submissionId, next),
    onSuccess: (updated) => {
      queryClient.setQueryData(
        queryKeys.submissions.detail(updated.id),
        updated,
      );
      onChanged();
      toast.success(`Статус изменён: ${statusLabel(updated.status)}`);
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const archiveMutation = useMutation({
    mutationFn: () => api.forms.archive(submissionId),
    onSuccess: () => {
      onChanged();
      toast.success("Заявка отправлена в архив");
      onBack();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const removeMutation = useMutation({
    mutationFn: () => api.forms.remove(submissionId),
    onSuccess: () => {
      onChanged();
      toast.success("Заявка скрыта из списка и экспорта");
      onBack();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  if (submissionQuery.isLoading) {
    return <Skeleton className="h-64 w-full" />;
  }

  if (submissionQuery.error) {
    return (
      <div className="space-y-4">
        <ErrorState
          error={submissionQuery.error}
          title="Не удалось загрузить заявку"
          onRetry={() => void submissionQuery.refetch()}
        />
        <Button variant="outline" onClick={onBack}>
          <ArrowLeft />
          К списку заявок
        </Button>
      </div>
    );
  }

  if (!submission) return null;

  const requestArchive = async () => {
    const confirmed = await confirm({
      title: "Отправить заявку в архив?",
      description:
        "Заявка исчезнет из списка и из экспорта, но останется в базе. Данные не удаляются.",
      confirmLabel: "В архив",
      destructive: true,
    });
    if (confirmed) archiveMutation.mutate();
  };

  const requestRemove = async () => {
    const confirmed = await confirm({
      title: "Удалить заявку из рабочего списка?",
      description:
        "Заявка исчезнет из списка, карточки и экспорта. Исходные данные останутся в базе как tombstone; восстановить заявку через админку нельзя.",
      confirmLabel: "Удалить",
      destructive: true,
    });
    if (confirmed) removeMutation.mutate();
  };

  return (
    <div className="page-grid">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <Button variant="ghost" size="sm" className="-ml-2" onClick={onBack}>
            <ArrowLeft />
            К списку заявок
          </Button>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <h1 className="text-lg font-semibold tracking-tight">Заявка</h1>
            <SubmissionStatusBadge status={submission.status} />
            {submission.archived_at && <Badge variant="muted">В архиве</Badge>}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            Получена {formatDateTime(submission.created_at)} · форма{" "}
            <code>{submission.feature_key}</code>
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {canManage && nextStatuses[submission.status].map((next) => (
            <Button
              key={next}
              variant={next === "processed" ? "default" : "outline"}
              size="sm"
              loading={statusMutation.isPending}
              onClick={() => statusMutation.mutate(next)}
            >
              {statusLabel(next)}
            </Button>
          ))}
          {canManage && <Button
            variant="outline"
            size="sm"
            loading={archiveMutation.isPending}
            onClick={() => void requestArchive()}
            disabled={Boolean(submission.archived_at)}
          >
            <Archive />
            В архив
          </Button>}
          {canDelete && (
            <Button
              variant="ghost"
              size="sm"
              className="text-destructive hover:text-destructive"
              loading={removeMutation.isPending}
              onClick={() => void requestRemove()}
            >
              <Trash2 />
              Удалить
            </Button>
          )}
        </div>
      </div>

      {schemaVersion ? (
        <InlineAlert tone="info" title="Данные по версии схемы на момент отправки">
          Показаны поля версии схемы v{schemaVersion.version}. Если схема формы с тех
          пор изменилась, структура заявки может отличаться от текущей.
        </InlineAlert>
      ) : (
        <InlineAlert tone="warning" title="Версия схемы не найдена">
          Не удалось сопоставить заявку с версией схемы. Данные показаны в исходном
          виде.
        </InlineAlert>
      )}

      <div className="rounded-lg border border-border bg-card">
        <div className="border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold">Данные заявки</h2>
        </div>
        {fields.length > 0 ? (
          <dl className="divide-y divide-border">
            {fields.map((fieldNode) => (
              <div
                key={fieldNode.id}
                className="grid gap-1 px-4 py-3 sm:grid-cols-[14rem_minmax(0,1fr)] sm:gap-4"
              >
                <dt className="text-xs font-medium text-muted-foreground">
                  {fieldNode.label || fieldNode.key}
                </dt>
                <dd className="min-w-0 text-sm">
                  <FieldValue value={submission.data[fieldNode.key]} />
                </dd>
              </div>
            ))}
            {Object.entries(submission.data)
              .filter(
                ([key]) => !fields.some((fieldNode) => fieldNode.key === key),
              )
              .map(([key, value]) => (
                <div
                  key={key}
                  className="grid gap-1 px-4 py-3 sm:grid-cols-[14rem_minmax(0,1fr)] sm:gap-4"
                >
                  <dt className="text-xs font-medium text-muted-foreground">
                    {key}
                  </dt>
                  <dd className="min-w-0 text-sm">
                    <FieldValue value={value} />
                  </dd>
                </div>
              ))}
          </dl>
        ) : (
          <pre className="overflow-x-auto px-4 py-3 text-xs leading-6">
            {JSON.stringify(submission.data, null, 2)}
          </pre>
        )}
      </div>
    </div>
  );
}

function FieldValue({ value }: { value: JsonValue | undefined }) {
  const text = valueToText(value);
  if (text.includes("\n")) {
    return (
      <pre className="overflow-x-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 px-2.5 py-2 text-2xs leading-5">
        {text}
      </pre>
    );
  }
  return <span className="break-words">{text}</span>;
}

function statusLabel(status: SubmissionStatus): string {
  return (
    {
      new: "Новая",
      in_progress: "В работе",
      processed: "Обработана",
      rejected: "Отклонена",
    }[status]
  );
}

import {
  Archive,
  CheckCircle2,
  CircleDashed,
  FileText,
  Inbox,
  Shield,
  XCircle,
} from "lucide-react";
import type {
  Feature,
  FeatureStatus,
  FeatureType,
  MediaStatus,
  Role,
  SubmissionStatus,
  Token,
  VersionStatus,
} from "@/types";
import { Badge } from "@/components/ui/badge";
import {
  featureStatusLabels,
  featureTypeLabels,
  mediaStatusLabels,
  roleLabels,
  submissionStatusLabels,
  versionStatusLabels,
} from "@/lib/format";

export function FeatureTypeBadge({ type }: { type: FeatureType }) {
  const Icon = type === "form" ? Inbox : FileText;
  return (
    <Badge variant={type === "form" ? "info" : "secondary"}>
      <Icon aria-hidden="true" />
      {featureTypeLabels[type]}
    </Badge>
  );
}

export function FeatureStatusBadge({ status }: { status: FeatureStatus }) {
  return (
    <Badge variant={status === "active" ? "success" : "muted"}>
      {status === "active" ? (
        <CheckCircle2 aria-hidden="true" />
      ) : (
        <Archive aria-hidden="true" />
      )}
      {featureStatusLabels[status]}
    </Badge>
  );
}

export function SchemaStateBadge({
  feature,
}: {
  feature: Pick<
    Feature,
    "prod_version_id" | "dev_version_id" | "draft_version_id"
  >;
}) {
  if (feature.prod_version_id && feature.draft_version_id) {
    return (
      <Badge variant="warning">
        <CircleDashed aria-hidden="true" />
        Есть неопубликованный черновик
      </Badge>
    );
  }
  if (feature.prod_version_id) {
    return (
      <Badge variant="success">
        <CheckCircle2 aria-hidden="true" />
        Опубликована
      </Badge>
    );
  }
  if (feature.dev_version_id && feature.draft_version_id) {
    return (
      <Badge variant="warning">
        <CircleDashed aria-hidden="true" />
        Черновик к схеме в dev
      </Badge>
    );
  }
  if (feature.dev_version_id) {
    // Published, but not yet promoted to prod.
    return (
      <Badge variant="success">
        <CheckCircle2 aria-hidden="true" />
        Схема в dev
      </Badge>
    );
  }
  if (feature.draft_version_id) {
    return (
      <Badge variant="warning">
        <CircleDashed aria-hidden="true" />
        Только черновик
      </Badge>
    );
  }
  return <Badge variant="muted">Нет схемы</Badge>;
}

export function VersionStatusBadge({ status }: { status: VersionStatus }) {
  return (
    <Badge variant={status === "published" ? "success" : "warning"}>
      {versionStatusLabels[status]}
    </Badge>
  );
}

export function MediaStatusBadge({ status }: { status: MediaStatus }) {
  const variant =
    status === "ready"
      ? "success"
      : status === "processing"
        ? "warning"
        : status === "failed"
          ? "destructive"
          : "muted";
  return <Badge variant={variant}>{mediaStatusLabels[status]}</Badge>;
}

export function SubmissionStatusBadge({ status }: { status: SubmissionStatus }) {
  const variant =
    status === "new"
      ? "default"
      : status === "in_progress"
        ? "warning"
        : status === "processed"
          ? "success"
          : "destructive";
  return <Badge variant={variant}>{submissionStatusLabels[status]}</Badge>;
}

export function RoleBadge({ role }: { role: Role }) {
  const variant = role === "admin" ? "default" : role === "moderator" ? "info" : "muted";
  return (
    <Badge variant={variant}>
      <Shield aria-hidden="true" />
      {roleLabels[role]}
    </Badge>
  );
}

export function TokenStatusBadge({ token }: { token: Pick<Token, "revoked_at" | "expires_at"> }) {
  if (token.revoked_at) {
    return (
      <Badge variant="destructive">
        <XCircle aria-hidden="true" />
        Отозван
      </Badge>
    );
  }
  if (token.expires_at && new Date(token.expires_at).getTime() <= Date.now()) {
    return <Badge variant="warning">Истёк</Badge>;
  }
  return (
    <Badge variant="success">
      <CheckCircle2 aria-hidden="true" />
      Активен
    </Badge>
  );
}

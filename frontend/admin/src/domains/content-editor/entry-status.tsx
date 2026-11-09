import { Archive, EyeOff, FileClock, FileText, PencilLine } from "lucide-react";
import type { EntryDetail } from "@/types";
import { Badge } from "@/components/ui/badge";

/**
 * Publication state of a content entry. The states are derived from the
 * pointers the backend returns, so the UI never invents a status.
 */
export type EntryPublishState =
  | "draft"
  | "published_dev"
  | "published"
  | "published_with_draft"
  | "unpublished"
  | "archived";

export function entryPublishState(entry: EntryDetail): EntryPublishState {
  if (entry.archived_at) return "archived";
  if (entry.prod_version_id && entry.draft_version_id) {
    return "published_with_draft";
  }
  if (entry.prod_version_id) return "published";
  if (entry.dev_version_id && entry.draft_version_id) {
    return "published_dev";
  }
  if (entry.dev_version_id) return "published_dev";
  if (entry.draft_version_id) return "draft";
  return "unpublished";
}

const stateConfig: Record<
  EntryPublishState,
  { label: string; variant: "default" | "secondary" | "outline" | "muted" | "destructive" | "success" | "warning" | "info" }
> = {
  draft: { label: "Только черновик", variant: "warning" },
  published_dev: { label: "Публикация в dev", variant: "success" },
  published: { label: "Опубликовано", variant: "success" },
  published_with_draft: { label: "Есть новый черновик", variant: "warning" },
  unpublished: { label: "Снято с публикации", variant: "muted" },
  archived: { label: "В архиве", variant: "destructive" },
};

const stateIcons: Record<EntryPublishState, typeof FileText> = {
  draft: FileClock,
  published_dev: FileClock,
  published: FileText,
  published_with_draft: PencilLine,
  unpublished: EyeOff,
  archived: Archive,
};

export function entryStateLabel(state: EntryPublishState): string {
  return stateConfig[state].label;
}

export function EntryPublishStateBadge({
  state,
  className,
}: {
  state: EntryPublishState;
  className?: string;
}) {
  const config = stateConfig[state];
  const Icon = stateIcons[state];
  return (
    <Badge variant={config.variant} className={className}>
      <Icon aria-hidden="true" />
      {config.label}
    </Badge>
  );
}

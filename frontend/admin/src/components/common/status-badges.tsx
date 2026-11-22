import type { MediaStatus } from "@/types";
import { Badge, type BadgeProps } from "@/components/ui/badge";
import { mediaStatusLabels } from "@/lib/format";

const mediaStatusVariants: Record<MediaStatus, BadgeProps["variant"]> = {
  ready: "success",
  processing: "warning",
  failed: "destructive",
  deleted: "muted",
};

export function MediaStatusBadge({ status }: { status: MediaStatus }) {
  return <Badge variant={mediaStatusVariants[status]}>{mediaStatusLabels[status]}</Badge>;
}

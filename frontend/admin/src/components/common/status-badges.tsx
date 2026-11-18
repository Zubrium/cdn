import type { MediaStatus } from "@/types";
import { Badge } from "@/components/ui/badge";
import { mediaStatusLabels } from "@/lib/format";

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

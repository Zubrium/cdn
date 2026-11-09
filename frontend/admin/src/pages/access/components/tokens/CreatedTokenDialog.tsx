import { useEffect, useState } from "react";
import { toast } from "sonner";
import type { CreatedToken } from "@/types";
import { formatDate, formatDateTime } from "@/lib/format";
import { InlineAlert } from "@/components/common/page";
import { RoleBadge } from "@/components/common/status-badges";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

type CreatedTokenDialogProps = {
  token: CreatedToken | null;
  open: boolean;
  onClose: () => void;
};

export function CreatedTokenDialog({ token, open, onClose }: CreatedTokenDialogProps) {
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    setVisible(false);
  }, [token]);

  if (!token) return null;

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>Токен создан</DialogTitle>
          <DialogDescription>
            Скопируйте секрет сейчас. После закрытия окна его невозможно получить
            снова — только отозвать токен и выпустить новый.
          </DialogDescription>
        </DialogHeader>

        <InlineAlert tone="warning" title="Секрет показывается один раз">
          Передайте токен владельцу по защищённому каналу и не сохраняйте его в
          репозитории или чатах.
        </InlineAlert>

        <div>
          <p className="mb-1 text-xs font-medium">Токен</p>
          <div className="rounded-md border border-border bg-muted/30 p-2">
            <code className="block break-all text-xs">
              {visible ? token.token : "•".repeat(Math.min(token.token.length, 48))}
            </code>
          </div>
          <div className="mt-2 flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setVisible((current) => !current)}
            >
              {visible ? "Скрыть" : "Показать"}
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                void navigator.clipboard?.writeText(token.token).then(
                  () => toast.success("Токен скопирован"),
                  () => toast.error("Не удалось скопировать"),
                );
              }}
            >
              Копировать
            </Button>
          </div>
        </div>

        <dl className="grid grid-cols-2 gap-2 text-xs">
          <div>
            <dt className="text-muted-foreground">Название</dt>
            <dd className="mt-0.5 font-medium">{token.name}</dd>
          </div>
          <div>
            <dt className="text-muted-foreground">Роль</dt>
            <dd className="mt-0.5">
              <RoleBadge role={token.role} />
            </dd>
          </div>
          <div>
            <dt className="text-muted-foreground">Создан</dt>
            <dd className="mt-0.5">{formatDateTime(token.created_at)}</dd>
          </div>
          <div>
            <dt className="text-muted-foreground">Действует до</dt>
            <dd className="mt-0.5">
              {token.expires_at ? formatDate(token.expires_at) : "Без срока"}
            </dd>
          </div>
        </dl>

        <DialogFooter>
          <Button onClick={onClose}>Готово</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

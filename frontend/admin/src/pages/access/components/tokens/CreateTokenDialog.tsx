import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/api";
import type { CreatedToken, Role } from "@/types";
import { useAuth } from "@/app/auth";
import { errorMessage } from "@/lib/errors";
import { roleLabels } from "@/lib/format";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

type CreateTokenDialogProps = {
  open: boolean;
  onClose: () => void;
  onCreated: (token: CreatedToken) => void;
};

export function CreateTokenDialog({ open, onClose, onCreated }: CreateTokenDialogProps) {
  const { principal } = useAuth();
  const isModerator = principal?.role === "moderator";
  const [name, setName] = useState("");
  const [role, setRole] = useState<Role>("user");
  const [expires, setExpires] = useState("");

  useEffect(() => {
    if (open) {
      setName("");
      setRole("user");
      setExpires("");
    }
  }, [open]);

  const createMutation = useMutation({
    mutationFn: () =>
      api.tokens.create({
        name: name.trim(),
        role,
        expires_at: expires
          ? new Date(`${expires}T23:59:59`).toISOString()
          : undefined,
      }),
    onSuccess: (created) => {
      onCreated(created);
      onClose();
    },
    onError: (error) => toast.error(errorMessage(error), { duration: 6000 }),
  });

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Новый токен доступа</DialogTitle>
          <DialogDescription>
            Токен задаёт доступ и роль учётной записи. Секрет показывается только
            один раз сразу после создания.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          <div>
            <label htmlFor="token-name" className="mb-1 block text-xs font-medium">
              Название
            </label>
            <Input
              id="token-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Например, Редактор контента"
              maxLength={80}
              autoFocus
            />
            <p className="mt-1 text-2xs text-muted-foreground">
              Имя видно в списке и в журналах. Не используйте секреты в названии.
            </p>
          </div>

          <div>
            <label htmlFor="token-role" className="mb-1 block text-xs font-medium">
              Роль
            </label>
            <Select
              value={role}
              onValueChange={(value) => setRole(value as Role)}
              disabled={isModerator}
            >
              <SelectTrigger id="token-role" aria-label="Роль токена">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="user">{roleLabels.user}</SelectItem>
                <SelectItem value="staff">{roleLabels.staff}</SelectItem>
                <SelectItem value="moderator">{roleLabels.moderator}</SelectItem>
                <SelectItem value="admin">{roleLabels.admin}</SelectItem>
              </SelectContent>
            </Select>
            {isModerator && (
              <p className="mt-1 text-2xs text-muted-foreground">
                Модератор может выпускать только токены пользователей.
              </p>
            )}
          </div>

          <div>
            <label htmlFor="token-expires" className="mb-1 block text-xs font-medium">
              Действует до
            </label>
            <Input
              id="token-expires"
              type="date"
              value={expires}
              onChange={(event) => setExpires(event.target.value)}
            />
            <p className="mt-1 text-2xs text-muted-foreground">
              Необязательно. Пустое поле — токен без срока действия.
            </p>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Отмена
          </Button>
          <Button
            onClick={() => createMutation.mutate()}
            disabled={!name.trim()}
            loading={createMutation.isPending}
          >
            <KeyRound />
            Создать
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

import { ErrorState, LoadingPage } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import type { ModelContentHistoryItem } from "@/types";

export interface ModelContentHistoryDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  items?: ModelContentHistoryItem[];
  loading: boolean;
  error: unknown;
  disabled: boolean;
  onRetry: () => void;
  onRestore: (publicationId: string) => void;
}

export function ModelContentHistoryDialog({
  open,
  onOpenChange,
  items,
  loading,
  error,
  disabled,
  onRetry,
  onRestore,
}: ModelContentHistoryDialogProps) {
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent>
      <DialogHeader><DialogTitle>История публикаций</DialogTitle><DialogDescription>Восстановление создаёт новый черновик. Для изменения публичных страниц его нужно опубликовать отдельно.</DialogDescription></DialogHeader>
      {loading && <LoadingPage label="Загружаем историю…" />}
      {Boolean(error) && <ErrorState error={error} title="Не удалось загрузить историю" onRetry={onRetry} />}
      {items?.length === 0 && !loading && !error && <p className="text-sm text-muted-foreground">Публикаций пока нет.</p>}
      {items && items.length > 0 && <ul className="max-h-80 space-y-2 overflow-auto">{items.map((item) => <li key={item.id} className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border p-3"><div className="text-sm"><p className="font-medium">Публикация {item.contentRevision}</p><p className="text-muted-foreground">{new Date(item.createdAt).toLocaleString("ru-RU")} · записей: {item.recordCount}</p></div><Button type="button" size="sm" variant="outline" disabled={disabled} onClick={() => onRestore(item.id)}>Восстановить</Button></li>)}</ul>}
      <DialogFooter><Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Закрыть</Button></DialogFooter>
    </DialogContent>
  </Dialog>;
}

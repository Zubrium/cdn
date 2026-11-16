import { Button } from "@/components/ui/button";

export function OrphanedSelectionsNotice({
  count,
  disabled,
  onClean,
}: {
  count: number;
  disabled: boolean;
  onClean: () => void;
}) {
  if (count === 0) return null;

  return (
    <section role="alert" aria-labelledby="orphaned-selections-title" className="space-y-3 rounded-xl border border-warning/50 bg-warning/10 p-4">
      <div>
        <h2 id="orphaned-selections-title" className="font-semibold">Устаревшие выборы размещений</h2>
        <p className="text-sm text-muted-foreground">
          Несовместимых выборов: {count}. Для них больше нет размещения или входа данных либо выбор продублирован. Они блокируют сохранение. Очистка затронет только черновик; опубликованные страницы изменятся после отдельной публикации.
        </p>
      </div>
      <Button type="button" variant="outline" className="min-h-11 text-destructive" disabled={disabled} onClick={onClean}>
        Очистить устаревшие выборы
      </Button>
    </section>
  );
}

import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, RefreshCw, Upload, X } from "lucide-react";
import { toast } from "sonner";
import { api, isUploadAccepted, uploadMediaFile } from "@/api";
import { useAuth } from "@/app/auth";
import { queryKeys } from "@/lib/query-keys";
import { cn } from "@/lib/utils";
import { useConfirm } from "@/components/common/confirm";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { formatBytes } from "@/lib/format";

type UploadTask = {
  id: string;
  file: File;
  group: string;
  progress: number | null;
  status: "pending" | "uploading" | "success" | "error" | "canceled";
  error?: string;
};

function taskId(file: File, index: number): string {
  return `${index}-${file.name}-${file.size}-${file.lastModified}`;
}

export function UploadDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const { principal } = useAuth();
  const [tasks, setTasks] = useState<UploadTask[]>([]);
  const [dragging, setDragging] = useState(false);
  const [group, setGroup] = useState("");
  const writableGroups = Object.entries(principal?.group_access ?? {})
    .filter(([, level]) => level === 2)
    .map(([key]) => key)
    .sort();
  const confirm = useConfirm();
  const limitsQuery = useQuery({ queryKey: ["file-limits"], queryFn: ({ signal }) => api.media.limits(signal), enabled: open });
  const controllers = useRef(new Map<string, AbortController>());
  const fileInput = useRef<HTMLInputElement>(null);
  const runningRef = useRef(false);

  useEffect(() => {
    controllers.current.forEach((controller) => controller.abort());
    controllers.current.clear();
    runningRef.current = false;
    setTasks([]);
    setDragging(false);
    setGroup("");
  }, [open]);

  const addFiles = useCallback((files: FileList | File[]) => {
    const incoming = Array.from(files);
    if (incoming.length === 0) return;
    if (!group) { toast.error("Выберите группу, в которую загрузить файлы"); return; }
    if (!limitsQuery.data) { toast.error("Не удалось получить ограничения загрузки"); return; }
    const maxBytes = limitsQuery.data.maxBytes;
    setTasks((current) => {
      const known = new Set(current.map((task) => task.id));
      const added: UploadTask[] = [];
      incoming.forEach((file, index) => {
        const id = taskId(file, current.length + index);
        if (known.has(id)) return;
        if (!isUploadAccepted(file)) {
          added.push({
            id,
            file,
            group,
            progress: null,
            status: "error",
            error: "Неподдерживаемый тип файла",
          });
          return;
        }
        if (file.size > maxBytes) {
          added.push({
            id,
            file,
            group,
            progress: null,
            status: "error",
            error: `Файл больше ${formatBytes(maxBytes)}`,
          });
          return;
        }
        added.push({ id, file, group, progress: null, status: "pending" });
      });
      return [...current, ...added];
    });
  }, [group, limitsQuery.data]);

  const uploadOne = useCallback(
    async (task: UploadTask) => {
      const controller = new AbortController();
      controllers.current.set(task.id, controller);
      setTasks((current) =>
        current.map((item) =>
          item.id === task.id
            ? { ...item, status: "uploading", progress: 0, error: undefined }
            : item,
        ),
      );
      try {
        await uploadMediaFile(
          task.file,
          [task.group],
          (progress) => {
            setTasks((current) =>
              current.map((item) =>
                item.id === task.id ? { ...item, progress } : item,
              ),
            );
          },
          controller.signal,
        );
        setTasks((current) =>
          current.map((item) =>
            item.id === task.id
              ? { ...item, status: "success", progress: 100 }
              : item,
          ),
        );
        void queryClient.invalidateQueries({ queryKey: queryKeys.media.all });
      } catch (error) {
        if (controller.signal.aborted) {
          setTasks((current) =>
            current.map((item) =>
              item.id === task.id ? { ...item, status: "canceled" } : item,
            ),
          );
        } else {
          setTasks((current) =>
            current.map((item) =>
              item.id === task.id
                ? {
                    ...item,
                    status: "error",
                    error: error instanceof Error ? error.message : "Ошибка загрузки",
                  }
                : item,
            ),
          );
        }
      } finally {
        controllers.current.delete(task.id);
      }
    },
    [queryClient],
  );

  // Upload files one by one. Any task that is still pending when a previous
  // upload finishes is picked up again on the next effect run.
  useEffect(() => {
    if (!open || runningRef.current) return;
    const next = tasks.find((task) => task.status === "pending");
    if (!next) return;
    runningRef.current = true;
    void uploadOne(next).finally(() => {
      runningRef.current = false;
      setTasks((current) => [...current]);
    });
  }, [open, tasks, uploadOne]);

  const cancelTask = (id: string) => {
    controllers.current.get(id)?.abort();
    setTasks(current => current.map(item => item.id === id && item.status === "pending" ? {...item, status:"canceled"} : item));
  };

  const cancelAll = () => {
    controllers.current.forEach((controller) => controller.abort());
    setTasks(current => current.map(item => item.status === "pending" ? {...item, status:"canceled"} : item));
  };

  const retry = (task: UploadTask) => {
    setTasks((current) =>
      current.map((item) =>
        item.id === task.id
          ? { ...item, status: "pending", progress: null, error: undefined }
          : item,
      ),
    );
  };

  const done = tasks.filter((task) => task.status === "success").length;
  const failed = tasks.filter((task) => task.status === "error").length;
  const active = tasks.some(
    (task) => task.status === "uploading" || task.status === "pending",
  );

  return (
    <Dialog open={open} onOpenChange={(next) => {
      if (next) return;
      if (!active) { onClose(); return; }
      void confirm({ title:"Отменить загрузку?", description:"Передача выбранных файлов будет остановлена.", confirmLabel:"Отменить загрузку", destructive:true }).then(accepted=>{if(accepted){cancelAll();onClose();}});
    }}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Загрузка медиа</DialogTitle>
          <DialogDescription>
            Любые файлы {limitsQuery.data ? `до ${formatBytes(limitsQuery.data.maxBytes)}` : "с ограничением, заданным сервером"}. Можно выбрать несколько файлов. При обрыве выберите тот же файл для продолжения.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-1.5">
          <label htmlFor="upload-group" className="text-sm font-medium">Группа доступа</label>
          <Select value={group} onValueChange={setGroup} disabled={active || writableGroups.length === 0}>
            <SelectTrigger id="upload-group"><SelectValue placeholder="Выберите группу" /></SelectTrigger>
            <SelectContent>{writableGroups.map((key) => <SelectItem key={key} value={key}>{key}</SelectItem>)}</SelectContent>
          </Select>
          {writableGroups.length === 0 && <p className="text-xs text-muted-foreground">Для загрузки нужна группа с уровнем доступа 2.</p>}
          {limitsQuery.error && <p className="text-sm text-destructive" role="alert">Не удалось загрузить ограничения. <button type="button" className="underline" onClick={()=>void limitsQuery.refetch()}>Повторить</button></p>}
        </div>
        <input
          ref={fileInput}
          type="file"
          multiple
          disabled={!group || !limitsQuery.data}
          className="sr-only"
          onChange={(event) => {
            if (event.target.files) addFiles(event.target.files);
            event.target.value = "";
          }}
          aria-label="Выбрать файлы"
        />

        <button
          type="button"
          disabled={!group || !limitsQuery.data}
          onClick={() => fileInput.current?.click()}
          onDragOver={(event) => {
            event.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={(event) => {
            event.preventDefault();
            setDragging(false);
            addFiles(event.dataTransfer.files);
          }}
          className={cn(
            "flex w-full cursor-pointer flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed p-6 text-center transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
            dragging
              ? "border-primary bg-primary/5"
              : "border-border bg-muted/20 hover:border-primary/40",
          )}
        >
          <Upload className="h-6 w-6 text-muted-foreground" aria-hidden="true" />
          <span className="text-sm font-medium">
            Перетащите файлы сюда или нажмите, чтобы выбрать
          </span>
          <span className="text-xs text-muted-foreground">
            Поддерживаются любые форматы, включая архивы, видео и документы
          </span>
        </button>

        {tasks.length > 0 && (
          <div className="max-h-64 space-y-2 overflow-y-auto thin-scrollbar">
            {tasks.map((task) => {
              const indeterminate = task.status === "uploading" && task.progress === null;
              const progressValue = task.status === "success" ? 100 : task.progress;
              return (
                <div
                  key={task.id}
                  className="rounded-md border border-border bg-card px-3 py-2"
                >
                  <div className="flex items-center gap-2">
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-xs font-medium" title={task.file.name}>
                        {task.file.name}
                      </p>
                      <p className="text-2xs text-muted-foreground">
                        {formatBytes(task.file.size)} · группа {task.group}
                        {task.error ? ` · ${task.error}` : ""}
                      </p>
                    </div>
                    <span className="shrink-0 text-2xs text-muted-foreground">
                      {task.status === "success"
                        ? "Готово"
                        : task.status === "error"
                          ? "Ошибка"
                          : task.status === "canceled"
                            ? "Отменено"
                            : task.status === "pending"
                              ? "В очереди"
                              : task.progress === null
                                ? "Загрузка…"
                                : `${task.progress}%`}
                    </span>
                    {task.status === "uploading" && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => cancelTask(task.id)}
                        aria-label={`Отменить загрузку ${task.file.name}`}
                      >
                        <X />
                      </Button>
                    )}
                    {(task.status === "error" || task.status === "canceled") && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => retry(task)}
                        aria-label={`Повторить загрузку ${task.file.name}`}
                      >
                        <RefreshCw />
                      </Button>
                    )}
                    {task.status === "success" && (
                      <Check className="h-4 w-4 shrink-0 text-success" aria-hidden="true" />
                    )}
                  </div>
                  {(task.status === "uploading" || task.status === "success") && (
                    <div
                      role="progressbar"
                      aria-label={`Загрузка ${task.file.name}`}
                      aria-valuemin={0}
                      aria-valuemax={100}
                      aria-valuenow={progressValue ?? undefined}
                      aria-valuetext={indeterminate ? "Загрузка…" : undefined}
                      aria-busy={indeterminate || undefined}
                      className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-border"
                    >
                      {indeterminate ? (
                        <div className="h-full w-full animate-pulse bg-primary/60" aria-hidden="true" />
                      ) : (
                        <div
                          className={cn(
                            "h-full transition-[width]",
                            task.status === "success" ? "bg-success" : "bg-primary",
                          )}
                          style={{ width: `${progressValue ?? 0}%` }}
                          aria-hidden="true"
                        />
                      )}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}

        <DialogFooter className="items-center sm:justify-between">
          <div className="text-xs text-muted-foreground">
            {tasks.length > 0
              ? `Готово: ${done} · Ошибок: ${failed}`
              : "Файлы ещё не выбраны"}
          </div>
          <div className="flex gap-2">
            {active ? (
              <Button variant="outline" onClick={cancelAll}>
                Отменить всё
              </Button>
            ) : (
              <Button variant="outline" onClick={onClose}>
                Закрыть
              </Button>
            )}
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

import { useEffect, useState } from "react";
import { FileUp, Route } from "lucide-react";
import type { Media } from "@/types";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { MediaPickerDialog } from "@/domains/media/MediaPickerDialog";
import { formatBytes } from "@/lib/format";
import { comparableRootPath, parseStaticFilePath } from "./route-utils";

export interface NewFileRoute {
  path: string;
  file: Media;
}

export function FileRouteDialog({
  open,
  existingPaths,
  onClose,
  onCreate,
}: {
  open: boolean;
  existingPaths: string[];
  onClose: () => void;
  onCreate: (route: NewFileRoute) => void;
}) {
  const [path, setPath] = useState("/file");
  const [file, setFile] = useState<Media | null>(null);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!open) return;
    setPath("/file");
    setFile(null);
    setError("");
    setPickerOpen(false);
  }, [open]);

  const submit = () => {
    const parsed = parseStaticFilePath(path);
    if (parsed.error || !parsed.path) {
      setError(parsed.error ?? "Введите корректный маршрут.");
      return;
    }
    if (!file) {
      setError("Выберите публичный файл.");
      return;
    }
    if (existingPaths.some((item) => comparableRootPath(item) === comparableRootPath(parsed.path!))) {
      setError("Такой маршрут уже используется.");
      return;
    }
    onCreate({ path: parsed.path, file });
  };

  return <>
    <Dialog open={open && !pickerOpen} onOpenChange={(next) => { if (!next) onClose(); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Новый файловый маршрут</DialogTitle>
          <DialogDescription>Фиксированный URL будет напрямую отдавать выбранный публичный файл.</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="file-route-path">Маршрут</Label>
            <Input id="file-route-path" className="min-h-11 font-mono" value={path} onChange={(event) => { setPath(event.currentTarget.value); setError(""); }} placeholder="/documents/price-list.pdf" />
            <p className="text-xs text-muted-foreground">Только фиксированный путь. Параметры, regex и catch-all не поддерживаются.</p>
          </div>
          <div className="space-y-2">
            <span className="block text-sm font-medium">Файл</span>
            <Button type="button" variant="outline" className="min-h-11 w-full justify-start" onClick={() => setPickerOpen(true)}>
              <FileUp />
              {file ? `${file.original_filename} · ${formatBytes(file.size_bytes)}` : "Выбрать публичный файл"}
            </Button>
          </div>
          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" className="min-h-11" onClick={onClose}>Отмена</Button>
          <Button type="button" className="min-h-11" disabled={!file || !path.trim()} onClick={submit}><Route />Создать маршрут</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
    <MediaPickerDialog
      open={pickerOpen}
      onClose={() => setPickerOpen(false)}
      onSelect={(items) => {
        if (items[0]) setFile(items[0]);
        setPickerOpen(false);
        setError("");
      }}
      accept="all"
      title="Выбрать публичный файл для маршрута"
      initialSelected={file ? [file.id] : []}
    />
  </>;
}

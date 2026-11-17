import { Link, useRouteError } from "react-router-dom";
import { FileQuestion } from "lucide-react";
import { EmptyState, useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";

export default function NotFoundPage() {
  useDocumentTitle("Страница не найдена");
  return (
    <div className="page-grid">
      <EmptyState
        icon={FileQuestion}
        title="Страница не найдена"
        description="Такого раздела в панели управления нет. Возможно, ссылка устарела."
        action={
          <Button asChild>
            <Link to="/">На главную</Link>
          </Button>
        }
        className="mt-10"
      />
    </div>
  );
}

export function RouteErrorPage() {
  const error = useRouteError();
  const message =
    error instanceof Error
      ? error.message
      : typeof error === "string"
        ? error
        : "Не удалось отобразить раздел.";
  return (
    <div className="flex min-h-dvh items-center justify-center bg-background p-4">
      <div className="w-full max-w-lg rounded-lg border border-destructive/30 bg-card p-6">
        <p className="text-2xs font-medium uppercase tracking-wider text-destructive">
          Ошибка интерфейса
        </p>
        <h1 className="mt-2 text-lg font-semibold">Что-то пошло не так</h1>
        <p className="mt-2 break-words text-sm leading-6 text-muted-foreground">
          {message}
        </p>
        <div className="mt-5 flex gap-2">
          <Button onClick={() => window.location.reload()}>Перезагрузить</Button>
          <Button variant="outline" asChild>
            <Link to="/">На главную</Link>
          </Button>
        </div>
      </div>
    </div>
  );
}

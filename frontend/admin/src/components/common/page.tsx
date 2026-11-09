import {
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Link } from "react-router-dom";
import {
  AlertTriangle,
  ArrowLeft,
  Check,
  Copy,
  Info,
  Inbox,
  Loader2,
  RefreshCw,
} from "lucide-react";
import { useBlocker } from "react-router-dom";
import { toast } from "sonner";
import type { LucideIcon } from "lucide-react";
import { errorDetails, errorMessage, requestIdOf } from "@/lib/errors";
import { copyToClipboard } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/misc";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { buttonVariants } from "@/components/ui/button";

type TooltipSide = "top" | "right" | "bottom" | "left";

/** Keeps section help on one tooltip surface across pointer, keyboard, and touch. */
export function ContextHelp({
  children,
  label = "Подсказка",
  side = "top",
  buttonClassName,
}: {
  children: ReactNode;
  label?: string;
  side?: TooltipSide;
  buttonClassName?: string;
}) {
  const [open, setOpen] = useState(false);
  const [compactViewport, setCompactViewport] = useState(() =>
    typeof window !== "undefined" && window.matchMedia("(max-width: 639px)").matches,
  );
  const [tooltipSide, setTooltipSide] = useState<TooltipSide>(() =>
    typeof window !== "undefined" && side === "right" && window.matchMedia("(max-width: 639px)").matches
      ? "top"
      : side,
  );
  const accessibleDescription = typeof children === "string" ? children : undefined;

  useEffect(() => {
    const media = window.matchMedia("(max-width: 639px)");
    const updateSide = () => {
      setCompactViewport(media.matches);
      setTooltipSide(media.matches && side === "right" ? "top" : side);
    };
    updateSide();
    media.addEventListener("change", updateSide);
    return () => media.removeEventListener("change", updateSide);
  }, [side]);

  return (
    <Tooltip open={open} onOpenChange={setOpen}>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={label}
          aria-description={accessibleDescription}
          aria-expanded={open}
          onClick={() => setOpen(true)}
          className={cn(
            "inline-flex size-6 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            buttonClassName,
          )}
        >
          <Info className="size-4" aria-hidden="true" />
        </button>
      </TooltipTrigger>
      <TooltipContent
        side={tooltipSide}
        sideOffset={8}
        style={compactViewport && side === "right" ? { maxWidth: "min(20rem, calc(100vw - 4rem))" } : undefined}
        className="max-w-sm whitespace-normal text-left leading-5"
      >
        {children}
      </TooltipContent>
    </Tooltip>
  );
}

export function PageHeader({
  title,
  description,
  actions,
  backTo,
  backLabel,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  backTo?: string;
  backLabel?: string;
  className?: string;
}) {
  return (
    <header
      className={cn(
        "flex flex-col gap-3 pb-6 xl:flex-row xl:items-start xl:justify-between",
        className,
      )}
    >
      <div className="flex min-w-0 items-start gap-2">
        <div className="min-w-0">
          {backTo && (
            <Link
              to={backTo}
              className="mb-2 inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
            >
              <ArrowLeft className="h-3.5 w-3.5" />
              {backLabel ?? "Назад"}
            </Link>
          )}
          <h1 className="break-words text-balance text-[1.6rem] font-semibold leading-tight tracking-[-0.035em] sm:text-[1.8rem]">
            {title}
          </h1>
        </div>
        {description && (
          <ContextHelp label="Подсказка по разделу" side="right" buttonClassName="mt-1.5 size-7">
            {description}
          </ContextHelp>
        )}
      </div>
      {actions && (
        <div className="flex min-w-0 flex-wrap items-center gap-2 xl:shrink-0 xl:justify-end">
          {actions}
        </div>
      )}
    </header>
  );
}

export function EmptyState({
  icon: Icon = Inbox,
  title,
  description,
  action,
  className,
}: {
  icon?: LucideIcon;
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center rounded-lg border border-dashed border-border bg-card/40 px-6 py-14 text-center",
        className,
      )}
    >
      <div className="mb-4 flex h-11 w-11 items-center justify-center rounded-full border border-border bg-muted/50">
        <Icon className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
      </div>
      <h3 className="text-base font-semibold">{title}</h3>
      {description && (
        <div className="mt-1.5 max-w-md text-sm leading-6 text-muted-foreground">
          {description}
        </div>
      )}
      {action && <div className="mt-5 flex flex-wrap justify-center gap-2">{action}</div>}
    </div>
  );
}

export function ErrorState({
  error,
  title = "Не удалось загрузить данные",
  onRetry,
  className,
}: {
  error: unknown;
  title?: string;
  onRetry?: () => void;
  className?: string;
}) {
  const details = errorDetails(error);
  const requestId = requestIdOf(error);
  return (
    <div
      role="alert"
      className={cn(
        "rounded-lg border border-destructive/30 bg-destructive/5 p-4 sm:p-5",
        className,
      )}
    >
      <div className="flex gap-3">
        <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-destructive" aria-hidden="true" />
        <div className="min-w-0">
          <p className="text-sm font-semibold">{title}</p>
          <p className="mt-1 text-sm leading-6 text-muted-foreground">
            {errorMessage(error)}
          </p>
          {details.length > 0 && (
            <ul className="mt-2 space-y-1 text-xs text-muted-foreground">
              {details.map((detail, index) => (
                <li key={`${detail.path ?? "field"}-${index}`}>
                  {detail.path && <code className="font-medium text-foreground">{detail.path}</code>}
                  {detail.path ? " — " : ""}
                  {detail.message}
                </li>
              ))}
            </ul>
          )}
          {requestId && (
            <p className="mt-2 text-2xs text-muted-foreground">
              ID запроса: <code>{requestId}</code>
            </p>
          )}
          {onRetry && (
            <Button
              variant="outline"
              size="sm"
              onClick={onRetry}
              className="mt-3 bg-background"
            >
              <RefreshCw />
              Повторить
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}

export function InlineAlert({
  tone = "info",
  title,
  children,
  actions,
  className,
}: {
  tone?: "info" | "warning" | "danger" | "success";
  title?: string;
  children: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  const toneClasses = {
    info: "border-info/30 bg-info/5 text-info",
    warning: "border-warning/35 bg-warning/8 text-warning",
    danger: "border-destructive/35 bg-destructive/5 text-destructive",
    success: "border-success/30 bg-success/5 text-success",
  }[tone];
  return (
    <div className={cn("rounded-lg border p-3.5 sm:p-4", toneClasses, className)}>
      <div className="flex gap-3">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
        <div className="min-w-0 flex-1 text-foreground">
          {title && <p className="text-sm font-semibold">{title}</p>}
          <div className={cn("text-sm leading-6 text-muted-foreground", title && "mt-1")}>
            {children}
          </div>
          {actions && <div className="mt-3 flex flex-wrap gap-2">{actions}</div>}
        </div>
      </div>
    </div>
  );
}

export function LoadingPage({ label = "Загрузка…" }: { label?: string }) {
  return (
    <div className="flex min-h-[40vh] items-center justify-center" role="status">
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
        {label}
      </div>
    </div>
  );
}

export function TableSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="space-y-2" aria-hidden="true">
      {Array.from({ length: rows }).map((_, index) => (
        <Skeleton key={index} className="h-11 w-full" />
      ))}
    </div>
  );
}

export function CopyButton({
  value,
  label = "Копировать",
  className,
  variant = "outline",
}: {
  value: string;
  label?: string;
  className?: string;
  variant?: "outline" | "ghost" | "secondary";
}) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1800);
    return () => window.clearTimeout(timer);
  }, [copied]);

  const handleCopy = async () => {
    try {
      await copyToClipboard(value);
      setCopied(true);
      toast.success("Скопировано в буфер обмена");
    } catch {
      toast.error("Не удалось скопировать в буфер обмена");
    }
  };

  return (
    <Button
      variant={variant}
      size="sm"
      onClick={() => void handleCopy()}
      className={className}
    >
      {copied ? <Check /> : <Copy />}
      {copied ? "Скопировано" : label}
    </Button>
  );
}

export function useDocumentTitle(title: string | undefined) {
  useEffect(() => {
    document.title = title ? `${title} — Zubrium` : "Zubrium";
  }, [title]);
}

/**
 * Blocks in-app navigation and browser unload while an editor has unsaved
 * local changes. The in-app dialog offers an explicit "leave" choice.
 *
 * `allowRef` lets the editor permit a programmatic navigation (e.g. replacing
 * `/content/new` with the URL of the entry it just created) even though the
 * `dirty` state has not re-rendered yet. The caller sets the flag right before
 * navigating; it is cleared automatically afterwards.
 */
export function useUnsavedChangesGuard(
  dirty: boolean,
  allowRef?: { current: boolean },
  options?: {
    allowPathChange?: (currentPath: string, nextPath: string) => boolean;
    protectSearch?: (currentSearch: string, nextSearch: string) => boolean;
  },
) {
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;
  const blocker = useBlocker(({ currentLocation, nextLocation }) => {
    if (allowRef?.current) {
      allowRef.current = false;
      return false;
    }
    const pathChanged = currentLocation.pathname !== nextLocation.pathname;
    const allowedPathChange = options?.allowPathChange?.(
      currentLocation.pathname,
      nextLocation.pathname,
    ) ?? false;
    const protectedSearchChange = Boolean(
      options?.protectSearch?.(currentLocation.search, nextLocation.search),
    );
    return dirtyRef.current && ((pathChanged && !allowedPathChange) || protectedSearchChange);
  });

  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);

  const dialog = (
    <AlertDialog open={blocker.state === "blocked"}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Есть несохранённые изменения</AlertDialogTitle>
          <AlertDialogDescription>
            Если уйти сейчас, несохранённые изменения будут потеряны. Останьтесь и сохраните их или покиньте страницу.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={() => blocker.reset?.()}>
            Остаться
          </AlertDialogCancel>
          <button
            type="button"
            className={cn(buttonVariants({ variant: "destructive" }))}
            onClick={() => blocker.proceed?.()}
          >
            Покинуть страницу
          </button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );

  return { dialog, blocked: blocker.state === "blocked" };
}

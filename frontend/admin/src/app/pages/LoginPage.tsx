import { useState } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import {
  Eye,
  EyeOff,
  KeyRound,
  Loader2,
  LockKeyhole,
  LogIn,
  RefreshCw,
  ShieldCheck,
} from "lucide-react";
import { useAuth } from "@/app/auth";
import { ApiError } from "@/api";
import { errorMessage } from "@/lib/errors";
import { roleLabels } from "@/lib/format";
import { useDocumentTitle } from "@/components/common/page";
import { BrandSymbol } from "@/components/common/brand-symbol";
import { ThemeToggle } from "@/components/common/theme-toggle";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";

const loginSchema = z.object({
  token: z.string().trim().min(1, "Введите Bearer-токен"),
});

type LoginValues = z.infer<typeof loginSchema>;

function BrandMark() {
  return (
    <div className="flex items-center gap-2.5">
      <BrandSymbol className="size-9 text-foreground" />
      <span className="text-sm font-semibold leading-tight">ZUBRIUM</span>
    </div>
  );
}

export function LoginPage() {
  useDocumentTitle("Вход");
  const auth = useAuth();
  const location = useLocation();
  const navigate = useNavigate();
  const [showToken, setShowToken] = useState(false);
  const [submitError, setSubmitError] = useState<unknown>(null);

  const state = location.state as { from?: string } | null;
  const from =
    state?.from && state.from.startsWith("/") && !state.from.startsWith("/login")
      ? state.from
      : "/";

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: { token: "" },
  });

  if (auth.status === "checking") {
    return (
      <div className="flex min-h-dvh items-center justify-center bg-background">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          Проверяем сессию…
        </div>
      </div>
    );
  }

  if (auth.status === "unavailable") {
    return (
      <div className="flex min-h-dvh flex-col bg-background">
        <div className="flex items-center justify-between px-4 py-4 sm:px-6">
          <BrandMark />
          <ThemeToggle />
        </div>
        <div className="flex flex-1 items-center justify-center px-4 pb-16">
          <div className="w-full max-w-md rounded-lg border border-border bg-card p-6">
            <div className="mb-4 flex h-10 w-10 items-center justify-center rounded-md bg-destructive/10 text-destructive">
              <ShieldCheck className="h-5 w-5" />
            </div>
            <h1 className="text-lg font-semibold">Сессия недоступна</h1>
            <p className="mt-2 text-sm leading-6 text-muted-foreground">
              Не удалось проверить сохранённый токен. Это может быть временная
              проблема сети или недоступность API.
            </p>
            {auth.error && (
              <p className="mt-3 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive">
                {auth.error}
              </p>
            )}
            <div className="mt-5 flex flex-wrap gap-2">
              <Button onClick={() => void auth.retry()}>
                <RefreshCw />
                Повторить
              </Button>
              <Button variant="outline" onClick={() => auth.logout()}>
                Войти другим токеном
              </Button>
            </div>
          </div>
        </div>
      </div>
    );
  }

  if (auth.status === "authenticated" && auth.principal) {
    return <Navigate to={from} replace />;
  }

  const onSubmit = async (values: LoginValues) => {
    setSubmitError(null);
    try {
      await auth.login(values.token);
      navigate(from, { replace: true });
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        setSubmitError(error);
      } else {
        setSubmitError(error);
      }
    }
  };

  return (
    <div className="grid min-h-dvh bg-background lg:grid-cols-[minmax(0,1fr)_30rem]">
      <section className="hidden flex-col justify-between border-r border-border bg-card px-10 py-10 lg:flex">
        <BrandMark />
        <div className="max-w-lg">
          <Badge variant="secondary" className="mb-4">
            <ShieldCheck />
            Защищённый доступ
          </Badge>
          <h1 className="text-balance text-3xl font-semibold tracking-tight">
            Панель управления сайтом
          </h1>
          <p className="mt-4 text-sm leading-7 text-muted-foreground">
            Страницы, контент и публикации — в одном рабочем месте.
          </p>
        </div>
        <p className="text-2xs font-semibold tracking-wide text-muted-foreground">Zubrium</p>
      </section>

      <main className="flex flex-col">
        <div className="flex items-center justify-between px-4 py-4 sm:px-6 lg:justify-end">
          <div className="lg:hidden">
            <BrandMark />
          </div>
          <ThemeToggle />
        </div>
        <div className="flex flex-1 items-center justify-center px-4 pb-16 sm:px-8">
          <div className="w-full max-w-sm">
            <div className="mb-6">
              <div className="mb-3 flex h-10 w-10 items-center justify-center rounded-md border border-border bg-muted/50">
                <LockKeyhole className="h-5 w-5 text-muted-foreground" />
              </div>
              <h1 className="text-xl font-semibold tracking-tight">
                Войти в Zubrium
              </h1>
              <p className="mt-1.5 text-sm leading-6 text-muted-foreground">
                Вставьте Bearer-токен, выданный администратором.
              </p>
            </div>

            {auth.notice && (
              <div className="mb-4 rounded-md border border-warning/30 bg-warning/5 px-3 py-2.5 text-xs leading-5 text-warning">
                {auth.notice}
              </div>
            )}

            {submitError !== null && (
              <div
                role="alert"
                className="mb-4 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2.5 text-xs leading-5 text-destructive"
              >
                {errorMessage(submitError)}
              </div>
            )}

            <form
              className="space-y-4"
              onSubmit={handleSubmit(onSubmit)}
              noValidate
            >
              <div className="space-y-2">
                <Label htmlFor="token">Bearer-токен</Label>
                <div className="relative">
                  <KeyRound className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    id="token"
                    type={showToken ? "text" : "password"}
                    autoComplete="off"
                    autoCapitalize="off"
                    spellCheck={false}
                    className="pl-9 pr-10 font-mono text-xs"
                    placeholder="ct_live_…"
                    aria-invalid={Boolean(errors.token)}
                    aria-describedby={errors.token ? "token-error" : undefined}
                    {...register("token", {
                      onChange: () => {
                        if (auth.notice) auth.clearNotice();
                      },
                    })}
                  />
                  <button
                    type="button"
                    className="absolute right-1.5 top-1/2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    onClick={() => setShowToken((value) => !value)}
                    aria-label={showToken ? "Скрыть токен" : "Показать токен"}
                  >
                    {showToken ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                  </button>
                </div>
                {errors.token && (
                  <p id="token-error" className="text-xs text-destructive">
                    {errors.token.message}
                  </p>
                )}
                <p className="text-2xs leading-5 text-muted-foreground">
                  Токен хранится только в этом браузере. Роль токена определяет
                  доступ к разделам.
                </p>
              </div>

              <Button type="submit" className="w-full" loading={isSubmitting}>
                {!isSubmitting && <LogIn />}
                Войти
              </Button>
            </form>

            <div className="mt-6 rounded-md border border-border bg-muted/30 p-3 text-2xs leading-5 text-muted-foreground">
              Пользовательские токены могут пройти проверку, но не получают доступ к
              администрированию. Это ограничение серверной части.
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}

export function NoAccessScreen() {
  useDocumentTitle("Нет доступа");
  const { principal, logout } = useAuth();
  return (
    <div className="flex min-h-dvh flex-col bg-background">
      <div className="flex items-center justify-between px-4 py-4 sm:px-6">
        <BrandMark />
        <ThemeToggle />
      </div>
      <div className="flex flex-1 items-center justify-center px-4 pb-16">
        <div className="w-full max-w-md rounded-lg border border-border bg-card p-6">
          <div className="mb-4 flex h-10 w-10 items-center justify-center rounded-md bg-muted">
            <LockKeyhole className="h-5 w-5 text-muted-foreground" />
          </div>
          <h1 className="text-lg font-semibold">Нет доступа к администрированию</h1>
          <p className="mt-2 text-sm leading-6 text-muted-foreground">
            Токен <strong className="text-foreground">{principal?.name}</strong>{" "}
            действителен, но имеет роль «
            {principal ? roleLabels[principal.role] : "Пользователь"}». Этой роли
            недостаточно для панели управления.
          </p>
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            Попросите администратора выпустить токен с ролью «Модератор» или
            «Администратор».
          </p>
          <Button className="mt-5" onClick={() => logout()}>
            Войти с другим токеном
          </Button>
        </div>
      </div>
    </div>
  );
}

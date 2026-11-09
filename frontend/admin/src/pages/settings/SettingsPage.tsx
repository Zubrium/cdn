import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Globe2, HardDrive, Lock, RefreshCw, Settings2, ShieldCheck } from "lucide-react";
import { api } from "@/api";
import {
  ErrorState,
  InlineAlert,
  LoadingPage,
  PageHeader,
  useDocumentTitle,
} from "@/components/common/page";
import { CopyButton } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Separator } from "@/components/ui/misc";
import { formatBytes } from "@/lib/format";

function SettingRow({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-0.5 py-2 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
      <div className="min-w-0">
        <dt className="text-sm">{label}</dt>
        {hint && <p className="text-2xs text-muted-foreground">{hint}</p>}
      </div>
      <dd className="shrink-0 text-sm font-medium sm:text-right">{children}</dd>
    </div>
  );
}

function PathRow({
  label,
  value,
  hint,
}: {
  label: string;
  value: string;
  hint?: string;
}) {
  return (
    <div className="flex flex-col gap-1.5 py-2 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
      <div className="min-w-0">
        <p className="text-sm">{label}</p>
        {hint && <p className="text-2xs text-muted-foreground">{hint}</p>}
      </div>
      <div className="flex min-w-0 items-center gap-1.5">
        <code className="block min-w-0 flex-1 truncate rounded bg-muted px-2 py-1 text-xs">{value}</code>
        <CopyButton value={value} label="" className="h-7 shrink-0 px-2" />
      </div>
    </div>
  );
}

export default function SettingsPage() {
  useDocumentTitle("Настройки");
  const settingsQuery = useQuery({
    queryKey: ["settings"],
    queryFn: ({ signal }) => api.settings.get(signal),
  });

  if (settingsQuery.isLoading) return <LoadingPage label="Загружаем настройки…" />;

  if (settingsQuery.error) {
    return (
      <div className="page-grid">
        <PageHeader title="Настройки" />
        <ErrorState
          error={settingsQuery.error}
          title="Не удалось загрузить настройки"
          onRetry={() => void settingsQuery.refetch()}
        />
      </div>
    );
  }

  const settings = settingsQuery.data;
  if (!settings) return null;
  const { limits, public_api, media } = settings;

  return (
    <div className="page-grid">
      <PageHeader
        title="Настройки"
        description="Настройки движка, API, медиа и доменов сайта."
        actions={
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" asChild>
              <Link to="/branches">Ветки</Link>
            </Button>
            <Button
              variant="outline"
              onClick={() => void settingsQuery.refetch()}
              loading={settingsQuery.isFetching}
            >
              <RefreshCw />
              Обновить
            </Button>
          </div>
        }
      />

      <InlineAlert tone="info" title="Настройки только для чтения">
        Лимиты задаются в конфигурации сервера. Секреты подключения сюда не передаются.
      </InlineAlert>

      <div className="grid min-w-0 grid-cols-1 gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <Settings2 className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
              <CardTitle>Ограничения</CardTitle>
            </div>
            <CardDescription>
              Лимиты, которые сервер проверяет при обработке запросов.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <dl className="divide-y divide-border">
              <SettingRow
                label="Максимальный размер JSON"
                hint="Тело запросов схем, записей и заявок"
              >
                {formatBytes(limits.max_json_bytes)}
              </SettingRow>
              <SettingRow
                label="Максимальный размер файла"
                hint="Загрузка в медиатеку"
              >
                {formatBytes(limits.max_upload_bytes)}
              </SettingRow>
              <SettingRow
                label="Лимит отправок формы"
                hint={`Окно ${limits.form_rate_window_seconds} с`}
              >
                {limits.form_rate_limit} запросов
              </SettingRow>
              <SettingRow label="Размер страницы в админке">
                {limits.admin_page_size} элементов
              </SettingRow>
              <SettingRow label="Размер публичной страницы">
                {limits.public_page_size} элементов
              </SettingRow>
              <SettingRow
                label="Максимум строк экспорта заявок"
                hint="Ограничение CSV-выгрузки"
              >
                {limits.submission_export_max_rows}
              </SettingRow>
            </dl>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <Globe2 className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
              <CardTitle>Публичный API</CardTitle>
            </div>
            <CardDescription>
              Базовые адреса, по которым контент и формы читает внешний сайт.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div className="divide-y divide-border">
              <PathRow
                label="Композиция страницы"
                value={public_api.runtime_path}
                hint="Замените {path} на запрашиваемый путь сайта"
              />
              <PathRow
                label="Заявки форм"
                value={public_api.forms_path}
                hint="Приём заявок от публичного сайта"
              />
              <PathRow label="Админ-API" value={public_api.admin_path} />
              <PathRow label="Медиа" value={public_api.media_path} />
            </div>
            <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3">
              <span className="text-xs text-muted-foreground">Источники CORS каждой ветки задаются в конфигурации запуска.</span>
              <Button variant="link" size="sm" asChild className="h-auto p-0">
                <Link to="/branches">Посмотреть ветки</Link>
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <HardDrive className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
              <CardTitle>Медиа</CardTitle>
            </div>
            <CardDescription>
              Допустимые форматы и варианты изображений. Ограничения совпадают с
              загрузчиком.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div>
              <p className="text-xs font-medium">Принимаемые типы</p>
              <div className="mt-1.5 flex flex-wrap gap-1">
                {media.accepted_types.map((type) => (
                  <Badge key={type} variant="muted">
                    {type}
                  </Badge>
                ))}
              </div>
            </div>
            <div className="flex items-center justify-between text-sm">
              <span>Максимальный размер</span>
              <span className="font-medium">{formatBytes(media.max_upload_bytes)}</span>
            </div>
            <div>
              <p className="text-xs font-medium">Варианты изображений</p>
              <div className="mt-1.5 flex flex-wrap gap-1">
                {media.image_variants.map((variant) => (
                  <Badge key={variant} variant="outline">
                    {variant}
                  </Badge>
                ))}
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <ShieldCheck className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
              <CardTitle>Доступ и безопасность</CardTitle>
            </div>
            <CardDescription>
              Роли и правила доступа определяются сервером и токенами.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <div className="flex items-center justify-between gap-3">
              <span>Роли в системе</span>
              <span className="font-medium">{settings.roles.length}</span>
            </div>
            <div className="flex flex-wrap gap-1">
              {settings.roles.map((role) => (
                <Badge key={role.id} variant="secondary">
                  {role.label}
                </Badge>
              ))}
            </div>
            <Separator />
            <div className="flex flex-col gap-2 sm:flex-row">
              <Button variant="outline" asChild className="flex-1">
                <Link to="/access/roles">
                  <ShieldCheck />
                  Матрица ролей
                </Link>
              </Button>
              <Button variant="outline" asChild className="flex-1">
                <Link to="/access/tokens">
                  <Lock />
                  Токены доступа
                </Link>
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

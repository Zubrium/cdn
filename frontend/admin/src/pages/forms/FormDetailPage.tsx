import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { RadioTower } from "lucide-react";
import { useNavigate, useParams } from "react-router-dom";
import { api } from "@/api";
import { useAuth } from "@/app/auth";
import { ErrorState, LoadingPage, PageHeader, useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { hasGroupAccess } from "@/lib/permissions";
import { FormSchemaEditor, PublishFormDialog } from "./FormEditor";
import { canonicalUUID, schemaDetailQueryKey } from "./forms-query";

export default function FormDetailPage({ creating = false }: { creating?: boolean }) {
  const { formId = "" } = useParams();
  const isCreating = creating || formId === "new";
  useDocumentTitle(isCreating ? "Новая форма" : "Форма");
  const { principal } = useAuth();
  const canManage = hasGroupAccess(principal, "forms_schemas", 2);
  const canPublish = canManage && hasGroupAccess(principal, "router_routes", 2);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [publishing, setPublishing] = useState(false);
  const detailKey = schemaDetailQueryKey(isCreating ? "new" : formId);
  const detail = useQuery({
    queryKey: detailKey,
    queryFn: ({ signal }) => api.formsEngine.schema(formId, signal),
    enabled: !isCreating && canonicalUUID.test(formId),
  });
  const publicationReady = canPublish && !isCreating && Boolean(detail.data);
  const operations = useQuery({
    queryKey: ["router", "operations"],
    queryFn: ({ signal }) => api.router.operations.list(signal),
    enabled: publicationReady && publishing,
  });
  const routes = useQuery({
    queryKey: ["router", "routes"],
    queryFn: ({ signal }) => api.router.routes.list({}, signal),
    enabled: publicationReady && publishing,
  });

  if (!isCreating && !canonicalUUID.test(formId)) {
    return <div className="page-grid">
      <ErrorState error={new Error("Некорректный идентификатор формы")} title="Форма не найдена" />
    </div>;
  }
  if (!isCreating && detail.isLoading) return <LoadingPage label="Загружаем форму…" />;
  if (!isCreating && detail.error) return <div className="page-grid">
    <ErrorState error={detail.error} title="Не удалось загрузить форму" onRetry={() => void detail.refetch()} />
  </div>;

  const schema = detail.data;
  const publicationError = publishing ? operations.error ?? routes.error : null;

  return <div className="page-grid">
    <PageHeader
      backTo="/forms"
      backLabel="Все формы"
      title={isCreating ? "Новая форма" : schema?.key ?? "Форма"}
      description={schema
        ? <>Ключ {schema.key} · Версия {schema.version} · ревизия {schema.revision}. Базовый путь публикации по умолчанию: /forms/{schema.key} · форма {schema.key} · версия {schema.version}.</>
        : "Создайте схему с уникальным глобальным ключом и явными типами полей."}
      actions={publicationReady && schema && <Button type="button" variant="outline" onClick={() => setPublishing(true)}><RadioTower />Опубликовать форму</Button>}
    />

    <FormSchemaEditor
      key={formId}
      schema={schema}
      canManage={canManage}
      onSaved={(saved) => {
        if (isCreating) {
          navigate(`/forms/${saved.id}`, { replace: true });
          return;
        }
        void queryClient.invalidateQueries({ queryKey: detailKey });
      }}
      onCancel={isCreating ? () => navigate("/forms") : undefined}
    />

    {publicationError && <p role="alert" className="text-sm text-destructive">Не удалось загрузить данные Router для публикации: {publicationError instanceof Error ? publicationError.message : String(publicationError)}</p>}
    {publishing && schema && (operations.data && routes.data
      ? <PublishFormDialog
          schema={schema}
          operations={operations.data.items}
          routes={routes.data.items}
          onClose={() => setPublishing(false)}
          onPublished={() => setPublishing(false)}
        />
      : publicationError ? null : <p role="status" className="text-sm text-muted-foreground">Загружаем данные Router…</p>)}
  </div>;
}

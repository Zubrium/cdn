import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, LockKeyhole, Plus, RefreshCw, ShieldCheck, UserRound, UsersRound } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/api";
import type { AccessGroup, CreatedPrincipal, GroupLevel, IssuedCredential, Principal } from "@/types";
import { useAuth } from "@/app/auth";
import { errorMessage } from "@/lib/errors";
import { copyToClipboard, formatDate, formatDateTime } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { EmptyState, ErrorState, InlineAlert, TableSkeleton } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const peopleKey = ["admin", "principals"] as const;
const groupsKey = ["admin", "access-groups"] as const;
const levels: Array<{ value: GroupLevel; label: string }> = [
  { value: 0, label: "Нет доступа" },
  { value: 1, label: "Чтение" },
  { value: 2, label: "Чтение и запись" },
];

function LevelSelect({ value, onChange, label }: { value: GroupLevel; onChange: (level: GroupLevel) => void; label: string }) {
  return (
    <select
      aria-label={label}
      value={value}
      onChange={(event) => onChange(Number(event.target.value) as GroupLevel)}
      className="h-11 min-w-40 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      {levels.map((level) => <option key={level.value} value={level.value}>{level.label}</option>)}
    </select>
  );
}

function CreatedCredentialDialog({ value, onClose }: { value: CreatedPrincipal | IssuedCredential | null; onClose: () => void }) {
  if (!value) return null;
  const name = "name" in value ? value.name : value.principal_id;
  const secret = "credential" in value ? value.credential : "";
  const expiresAt = "expires_at" in value ? value.expires_at : undefined;
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Ключ доступа создан</DialogTitle>
          <DialogDescription>Секрет виден только сейчас. Скопируйте его и передайте владельцу защищённым способом.</DialogDescription>
        </DialogHeader>
        <InlineAlert tone="warning" title="Показывается один раз">
          После закрытия окна Core не сможет восстановить этот секрет. Для следующего входа потребуется выпустить новый ключ.
        </InlineAlert>
        <div className="rounded-md border border-border bg-muted/30 p-3">
          <code className="block break-all text-xs">{secret || "Секрет не вернулся от сервера"}</code>
        </div>
        <dl className="grid grid-cols-2 gap-3 text-xs">
          <div><dt className="text-muted-foreground">Учётная запись</dt><dd className="mt-1 font-medium">{name}</dd></div>
          <div><dt className="text-muted-foreground">Действует до</dt><dd className="mt-1 font-medium">{expiresAt ? formatDate(expiresAt) : "Без срока"}</dd></div>
        </dl>
        <DialogFooter>
          <Button variant="outline" onClick={() => void copyToClipboard(secret).then(() => toast.success("Ключ скопирован"), () => toast.error("Не удалось скопировать ключ"))} disabled={!secret}>Копировать</Button>
          <Button onClick={onClose}>Готово</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function CreatePrincipalDialog({ open, groups, onClose, onCreated }: {
  open: boolean;
  groups: AccessGroup[];
  onClose: () => void;
  onCreated: (principal: CreatedPrincipal) => void;
}) {
  const [name, setName] = useState("");
  const [expires, setExpires] = useState("");
  const [canManageAdministrators, setCanManageAdministrators] = useState(false);
  const [groupAccess, setGroupAccess] = useState<Record<string, GroupLevel>>({});
  const queryClient = useQueryClient();
  const writableGroups = groups.filter((group) => group.active && group.level === 2);
  const createMutation = useMutation({
    mutationFn: () => api.principals.create({
      name: name.trim(),
      can_manage_administrators: canManageAdministrators,
      group_access: Object.fromEntries(Object.entries(groupAccess).filter(([, level]) => level !== 0)),
      expires_at: expires ? new Date(`${expires}T23:59:59`).toISOString() : undefined,
    }),
    onSuccess: (created) => {
      void queryClient.invalidateQueries({ queryKey: peopleKey });
      onCreated(created);
      setName("");
      setExpires("");
      setCanManageAdministrators(false);
      setGroupAccess({});
      onClose();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>Новая учётная запись</DialogTitle>
          <DialogDescription>Создатель явно назначает только те группы, в которых у него есть уровень 2. Остальные начинаются с уровня 0.</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <label className="block space-y-1.5 text-sm">
            <span className="font-medium">Имя</span>
            <Input autoFocus maxLength={120} value={name} onChange={(event) => setName(event.target.value)} placeholder="Имя человека или интеграции" />
          </label>
          <label className="flex min-h-11 items-center gap-2 text-sm">
            <input type="checkbox" checked={canManageAdministrators} onChange={(event) => setCanManageAdministrators(event.target.checked)} className="size-4 accent-primary" />
            <span>Может управлять учётными записями и ключами</span>
          </label>
          <label className="block space-y-1.5 text-sm">
            <span className="font-medium">Срок действия учётной записи</span>
            <Input type="date" value={expires} onChange={(event) => setExpires(event.target.value)} />
            <span className="block text-xs text-muted-foreground">Необязательно. Пустое значение не задаёт срок.</span>
          </label>
          {writableGroups.length > 0 ? (
            <fieldset className="space-y-2">
              <legend className="text-sm font-medium">Права на группы</legend>
              {writableGroups.map((group) => (
                <div key={group.key} className="flex flex-col gap-2 rounded-md border border-border p-3 sm:flex-row sm:items-center sm:justify-between">
                  <span className="text-sm font-medium">{group.key}</span>
                  <LevelSelect label={`Доступ к группе ${group.key}`} value={groupAccess[group.key] ?? 0} onChange={(level) => setGroupAccess((current) => ({ ...current, [group.key]: level }))} />
                </div>
              ))}
            </fieldset>
          ) : (
            <p className="rounded-md border border-border bg-muted/30 p-3 text-sm text-muted-foreground">У вас нет уровня 2 ни в одной доступной группе, поэтому новые права можно будет назначить позже.</p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Отмена</Button>
          <Button onClick={() => createMutation.mutate()} disabled={!name.trim()} loading={createMutation.isPending}><Plus />Создать</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function GroupAccessDialog({ principal, groups, onClose }: { principal: Principal | null; groups: AccessGroup[]; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [values, setValues] = useState<Record<string, GroupLevel>>({});
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const principalID = principal?.id;
  const writableGroups = useMemo(() => groups.filter((group) => group.active && group.level === 2), [groups]);
  useEffect(() => {
    let cancelled = false;
    if (!principalID || writableGroups.length === 0) return;
    setLoading(true);
    setError(null);
    void Promise.all(writableGroups.map(async (group) => {
      const result = await api.principals.groupAccess(principalID, group.key);
      return [group.key, result.level] as const;
    })).then((entries) => {
      if (!cancelled) setValues(Object.fromEntries(entries));
    }).catch((loadError: unknown) => {
      if (!cancelled) setError(errorMessage(loadError));
    }).finally(() => {
      if (!cancelled) setLoading(false);
    });
    return () => { cancelled = true; };
  }, [principalID, writableGroups]);
  const saveMutation = useMutation({
    mutationFn: ({ group, level }: { group: string; level: GroupLevel }) => api.principals.setGroupAccess(principal!.id, group, level),
    onSuccess: async (_data, variables) => {
      toast.success(`Доступ к группе «${variables.group}» обновлён`);
      await queryClient.invalidateQueries({ queryKey: peopleKey });
    },
    onError: (saveError) => toast.error(errorMessage(saveError)),
  });
  return (
    <Dialog open={Boolean(principal)} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>Права по группам</DialogTitle>
          <DialogDescription>{principal?.name}. Уровень 2 в группе позволяет изменять назначения этой группы.</DialogDescription>
        </DialogHeader>
        {loading ? <p className="text-sm text-muted-foreground">Загружаем назначения…</p> : error ? <p role="alert" className="text-sm text-destructive">{error}</p> : writableGroups.length === 0 ? (
          <p className="text-sm text-muted-foreground">Нет групп, где у вашей учётной записи есть уровень 2.</p>
        ) : (
          <div className="space-y-2">
            {writableGroups.map((group) => (
              <div key={group.key} className="flex flex-col gap-2 rounded-md border border-border p-3 sm:flex-row sm:items-center sm:justify-between">
                <span className="text-sm font-medium">{group.key}</span>
                <div className="flex flex-wrap items-center gap-2">
                  <LevelSelect label={`Доступ ${principal?.name} к группе ${group.key}`} value={values[group.key] ?? 0} onChange={(level) => setValues((current) => ({ ...current, [group.key]: level }))} />
                  <Button size="sm" variant="outline" disabled={values[group.key] === undefined || saveMutation.isPending} loading={saveMutation.isPending} onClick={() => saveMutation.mutate({ group: group.key, level: values[group.key]! })}>Сохранить</Button>
                </div>
              </div>
            ))}
          </div>
        )}
        <DialogFooter><Button variant="outline" onClick={onClose}>Закрыть</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function CredentialsDialog({ principal, onClose, onSecret }: { principal: Principal | null; onClose: () => void; onSecret: (value: IssuedCredential) => void }) {
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const queryKey = ["admin", "credentials", principal?.id] as const;
  const credentials = useQuery({ queryKey, queryFn: ({ signal }) => api.principals.credentials(principal!.id, signal), enabled: Boolean(principal) });
  const issueMutation = useMutation({
    mutationFn: () => api.principals.issueCredential(principal!.id),
    onSuccess: async (issued) => {
      await queryClient.invalidateQueries({ queryKey });
      onSecret(issued);
    },
    onError: (error) => toast.error(errorMessage(error)),
  });
  const revokeMutation = useMutation({
    mutationFn: (id: string) => api.principals.revokeCredential(id),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey }),
    onError: (error) => toast.error(errorMessage(error)),
  });
  const revoke = async (credential: { id: string; revoked_at: string | null }) => {
    if (credential.revoked_at) return;
    const accepted = await confirm({ title: "Отозвать ключ?", description: "Ключ перестанет работать немедленно. Восстановить его нельзя.", confirmLabel: "Отозвать", destructive: true });
    if (accepted) revokeMutation.mutate(credential.id);
  };
  return (
    <Dialog open={Boolean(principal)} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader><DialogTitle>Ключи доступа</DialogTitle><DialogDescription>{principal?.name}. Секрет каждого ключа отображается только один раз.</DialogDescription></DialogHeader>
        {credentials.isLoading ? <p className="text-sm text-muted-foreground">Загружаем ключи…</p> : credentials.error ? <p role="alert" className="text-sm text-destructive">{errorMessage(credentials.error)}</p> : (credentials.data?.length ?? 0) === 0 ? <p className="text-sm text-muted-foreground">У этой учётной записи пока нет ключей.</p> : (
          <ul className="divide-y divide-border rounded-md border border-border">
            {credentials.data?.map((credential) => {
              const revoked = Boolean(credential.revoked_at);
              const expired = Boolean(credential.expires_at && Date.parse(credential.expires_at) <= Date.now());
              return <li key={credential.id} className="flex flex-col gap-2 p-3 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0"><code className="block truncate text-xs">{credential.id}</code><span className="mt-1 block text-xs text-muted-foreground">Создан {formatDateTime(credential.created_at)} · {credential.expires_at ? `до ${formatDate(credential.expires_at)}` : "без срока"}</span></div>
                <div className="flex items-center gap-2">{revoked ? <Badge variant="destructive">Отозван</Badge> : expired ? <Badge variant="warning">Истёк</Badge> : <Badge variant="success">Активен</Badge>}<Button size="sm" variant="ghost" disabled={revoked || revokeMutation.isPending} onClick={() => void revoke(credential)}><LockKeyhole />Отозвать</Button></div>
              </li>;
            })}
          </ul>
        )}
        <DialogFooter><Button variant="outline" onClick={onClose}>Закрыть</Button><Button onClick={() => issueMutation.mutate()} loading={issueMutation.isPending}><KeyRound />Выпустить ключ</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default function UsersPage() {
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const { principal: current } = useAuth();
  const [createOpen, setCreateOpen] = useState(false);
  const [created, setCreated] = useState<CreatedPrincipal | IssuedCredential | null>(null);
  const [groupPrincipal, setGroupPrincipal] = useState<Principal | null>(null);
  const [credentialsPrincipal, setCredentialsPrincipal] = useState<Principal | null>(null);
  const people = useQuery({ queryKey: peopleKey, queryFn: ({ signal }) => api.principals.list(signal) });
  const groups = useQuery({ queryKey: groupsKey, queryFn: ({ signal }) => api.principals.groups(signal) });
  const managerMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => api.principals.setAdministratorManagement(id, enabled),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: peopleKey }),
    onError: (error) => toast.error(errorMessage(error)),
  });
  const deactivateMutation = useMutation({
    mutationFn: (id: string) => api.principals.deactivate(id),
    onSuccess: () => { toast.success("Учётная запись отключена"); void queryClient.invalidateQueries({ queryKey: peopleKey }); },
    onError: (error) => toast.error(errorMessage(error)),
  });
  const writableGroups = (groups.data ?? []).filter((group) => group.active && group.level === 2);
  const principals = people.data ?? [];

  const deactivate = async (target: Principal) => {
    const accepted = await confirm({
      title: "Отключить учётную запись?",
      description: <p>«{target.name}» и все его ключи потеряют доступ к системе.</p>,
      confirmLabel: "Отключить",
      destructive: true,
    });
    if (accepted) deactivateMutation.mutate(target.id);
  };

  return (
    <div className="space-y-4">
      <header className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div><h2 className="text-lg font-semibold tracking-tight">Учётные записи</h2><p className="mt-1 text-sm text-muted-foreground">Люди и интеграции используют один тип пользователя. Доступ задаётся назначениями групп.</p></div>
        <div className="flex gap-2"><Button variant="outline" onClick={() => void people.refetch()} loading={people.isFetching}><RefreshCw />Обновить</Button><Button onClick={() => setCreateOpen(true)}><Plus />Создать</Button></div>
      </header>

      <InlineAlert tone="info" title="Уровни доступа">
        0 — доступа нет, 1 — чтение, 2 — чтение и запись. Только пользователь с отдельным правом управления администраторами может создавать учётные записи и выдавать ключи.
      </InlineAlert>

      {groups.error && <p role="alert" className="text-sm text-destructive">Не удалось получить список групп: {errorMessage(groups.error)}</p>}
      {people.isLoading ? <TableSkeleton rows={5} /> : people.error ? <ErrorState error={people.error} onRetry={() => void people.refetch()} /> : principals.length === 0 ? (
        <EmptyState icon={UsersRound} title="Учётных записей пока нет" description="Создайте первую запись и задайте ей необходимые группы." action={<Button onClick={() => setCreateOpen(true)}><Plus />Создать</Button>} />
      ) : (
        <div className="table-wrap">
          <table className="data-table min-w-[760px]">
            <thead><tr><th scope="col">Имя</th><th scope="col">Статус</th><th scope="col">Управление учётными записями</th><th scope="col"><span className="sr-only">Действия</span></th></tr></thead>
            <tbody>{principals.map((person) => (
              <tr key={person.id}>
                <td><div className="flex items-center gap-2"><UserRound className="size-4 text-muted-foreground" /><span className="font-medium">{person.name}</span>{person.id === current?.id && <Badge variant="secondary">Вы</Badge>}</div></td>
                <td>{person.active ? <Badge variant="success">Активна</Badge> : <Badge variant="destructive">Отключена</Badge>}</td>
                <td>{person.can_manage_administrators ? <Badge variant="info"><ShieldCheck />Есть</Badge> : <span className="text-sm text-muted-foreground">Нет</span>}</td>
                <td><div className="flex flex-wrap justify-end gap-1">
                  <Button size="sm" variant="outline" onClick={() => setGroupPrincipal(person)} disabled={writableGroups.length === 0}>Группы</Button>
                  <Button size="sm" variant="outline" onClick={() => setCredentialsPrincipal(person)}><KeyRound />Ключи</Button>
                  <Button size="sm" variant="ghost" disabled={managerMutation.isPending} onClick={() => managerMutation.mutate({ id: person.id, enabled: !person.can_manage_administrators })}>{person.can_manage_administrators ? "Снять управление" : "Выдать управление"}</Button>
                  {person.active && <Button size="sm" variant="ghost" className="text-destructive" disabled={deactivateMutation.isPending} onClick={() => void deactivate(person)}>Отключить</Button>}
                </div></td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      )}

      <CreatePrincipalDialog open={createOpen} groups={groups.data ?? []} onClose={() => setCreateOpen(false)} onCreated={setCreated} />
      <GroupAccessDialog principal={groupPrincipal} groups={groups.data ?? []} onClose={() => setGroupPrincipal(null)} />
      <CredentialsDialog principal={credentialsPrincipal} onClose={() => setCredentialsPrincipal(null)} onSecret={setCreated} />
      <CreatedCredentialDialog value={created} onClose={() => setCreated(null)} />
    </div>
  );
}

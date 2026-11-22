# Browser QA в отдельной копии чекаута

Скрипты подготавливают изолированную копию чекаута и проверяют, что браузерные
тесты направлены именно в неё, прежде чем выполнять сценарии с записью. Они не
запускают серверы и браузер и не добавляют Node/Playwright-зависимостей.

Требования: macOS (`ditto`), `git`, `make`, `go`, `pnpm`, `python3`, `lsof`, `ps`.
Копия включает uncommitted и untracked файлы, поэтому может содержать секреты:
храни её в приватном каталоге, не выводи содержимое в чат/отчёты и удали после
прогона.

## 1. Подготовить копию

```sh
umask 077
cd "/Users/docup/Projects/website engine"
SRC="$PWD"
CHECKOUT="$(tests/qa/browser/prepare-checkout.sh)"
RUN_DIR="$(dirname "$CHECKOUT")"
printf '%s\n' "$CHECKOUT"
```

`prepare-checkout.sh` печатает в stdout только путь созданной копии. По умолчанию
это `$TMPDIR/website-engine-qa.XXXXXX/checkout`. Можно передать явный путь:
`prepare-checkout.sh /path/to/new/qa-checkout` — скрипт откажется, если цель уже
существует, если она внутри исходного чекаута или если это linked worktree.
До копирования и после него сравниваются `git rev-parse HEAD` и
`git status --porcelain`; исходный чекаут не изменяется.

## 2. Собрать Admin-ассеты в копии

```sh
cd "$CHECKOUT/frontend"
pnpm install --frozen-lockfile   # только если node_modules нет в копии
pnpm --filter @website-engine/admin build
```

Build пишет `pkg/static/dist` только в копии.

## 3. Сгенерировать профили и собрать product-бинарник

```sh
cd "$CHECKOUT"
make -C main generate-profiles
make build-full
mkdir -p "$CHECKOUT/bin"
go build -tags=full -o "$CHECKOUT/bin/zubrium" ./main/cmd/zubrium
```

`make build-full` проверяет сборку; отдельная команда создаёт
`$CHECKOUT/bin/zubrium` — именно этот путь проверяет preflight. Полный профиль
поднимает Admin и публичный Router в одном процессе.

## 4. Конфигурация с уникальными портами

```sh
ADMIN_PORT=18080
PUBLIC_PORT=18081
cp "$CHECKOUT/main/config.example.yaml" "$CHECKOUT/main/qa-browser.yaml"

python3 - "$CHECKOUT/main/qa-browser.yaml" "$ADMIN_PORT" "$PUBLIC_PORT" <<'PY'
import re
import sys

path, admin, public = sys.argv[1:]
with open(path, encoding="utf-8") as source:
    text = source.read()


def replace_once(pattern, value, label):
    global text
    text, count = re.subn(pattern, lambda match: match.group(1) + value, text, count=1, flags=re.M)
    if count != 1:
        raise SystemExit("cannot patch " + label)


replace_once(r"^(\s*listen_address:\s*).*$", "127.0.0.1:" + admin, "http.listen_address")
replace_once(r"^(\s*public_listen_address:\s*).*$", "127.0.0.1:" + public, "router_api.public_listen_address")
text, count = re.subn(r"http://127\.0\.0\.1:8080", "http://127.0.0.1:" + admin, text)
if count < 1:
    raise SystemExit("cannot patch admin_api.allowed_origins")

with open(path, "w", encoding="utf-8") as target:
    target.write(text)
PY
```

Порты обязательно должны отличаться от рабочих `8080`, `5173`, `55432` и друг
от друга. В YAML задай только несекретные параметры одноразового окружения:
`core.s3_endpoint`, `core.s3_bucket`, `router_api.upload_staging_directory`.
Секреты в YAML не пиши; экспортируй их из секретного хранилища до запуска:

```text
ZUBRIUM_DATABASE_URL
ZUBRIUM_MIGRATION_DATABASE_URL
ZUBRIUM_S3_ACCESS_KEY
ZUBRIUM_S3_SECRET_KEY
ZUBRIUM_ROUTER_SERVICE_CREDENTIAL
ZUBRIUM_FORMS_FILE_SERVICE_CREDENTIAL
ZUBRIUM_CONTENT_SERVICE_CREDENTIAL
```

Используй только БД, bucket и atlas-бинарник, созданные для этого прогона.
До запуска запиши их точные имена и порты, чтобы затем удалить только их.

## 5. Запустить копию и выполнить preflight

```sh
BUILD_IDENTITY="browser-qa-$(date +%Y%m%d%H%M%S)"
export ZUBRIUM_BUILD_IDENTITY="$BUILD_IDENTITY"
cd "$CHECKOUT"
"$CHECKOUT/bin/zubrium" --config "$CHECKOUT/main/qa-browser.yaml" \
  >"$RUN_DIR/zubrium.log" 2>&1 &
SERVER_PID=$!

QA_BROWSER_CONFIG="$CHECKOUT/main/qa-browser.yaml" \
  EXPECTED_BUILD_IDENTITY="$BUILD_IDENTITY" \
  "$SRC/tests/qa/browser/verify-target.sh" "$CHECKOUT" "$ADMIN_PORT" "$PUBLIC_PORT"
```

Вместо `EXPECTED_BUILD_IDENTITY` можно передать то же значение четвёртым
аргументом: `verify-target.sh "$CHECKOUT" "$ADMIN_PORT" "$PUBLIC_PORT" "$BUILD_IDENTITY"`.
Ожидаемое значение обязательно: без аргумента и без env проверка завершается
ошибкой. `ZUBRIUM_BUILD_IDENTITY` должно быть экспортировано до запуска сервера,
иначе заголовок не будет выставлен.

Лог может содержать одноразовый секрет администратора: не выводи его в чат и не
копируй в отчёты. `verify-target.sh` ничего не печатает из command line,
конфигурации, заголовков, тела ответа или самого build identity. При любом
несовпадении он возвращает ненулевой код — сценарии с записью в этом случае не
запускай. Проверку нужно повторить после каждого рестарта сервера.

Скрипт проверяет:

- на обоих портах слушает процесс с command line, содержащей абсолютный путь
  `$CHECKOUT/bin/zubrium` и аргумент `--config` с конфигом внутри этой копии;
- `GET /healthz` и `GET /readyz` на Admin-порту возвращают `204`;
- `X-Zubrium-Build-Identity` в ответе Admin-порта на `GET /healthz` присутствует
  ровно один раз и совпадает с ожидаемым значением;
- для публичного listener'а та же проверка применяется, если он отдаёт
  `GET /healthz` с кодом `204` или возвращает этот заголовок; если эндпоинт
  отсутствует, скрипт явно сообщает, что проверка неприменима;
- на рабочих портах `8080`, `5173`, `55432` нет слушателей.

Отсутствие заголовка там, где он ожидается, и любое расхождение значения
трактуются как провал (fail closed).

## 6. Браузерные сценарии

Используй уже доступный браузерный tooling родительской сессии, без установки
новых зависимостей.

- **Admin:** открой `http://127.0.0.1:$ADMIN_PORT/admin`, войди под одноразовым
  administrator secret из защищённого лога. Проверь site/routes/placements,
  content drafts и publication, snapshots, feature arguments и manifest-driven
  UI, forms, router routes, design tokens и coordinator. Сценарии с записью
  выполняй только против disposable PostgreSQL/S3 этого прогона.
- **Public runtime:** открой `http://127.0.0.1:$PUBLIC_PORT/<configured-route>`
  и проверь рендер фич, формы и ассеты. Не подставляй в URL Core ID, group ID,
  FileSet ID или произвольные operation ID.
- Результаты фиксируй в `tests/ACCEPTANCE-MATRIX.md` и `docs/QA_REPORT.md` как
  значимые input/result сценарии. Не помечай сценарий как пройденный, если
  preflight не выполнялся или указывал на другой чекаут.

## 7. Очистка только созданного этим прогоном

```sh
kill "$SERVER_PID" 2>/dev/null || true
wait "$SERVER_PID" 2>/dev/null || true
```

Затем удали строго те disposable database/S3 bucket/objects и staging-каталог,
имена которых были зафиксированы перед шагом 5, и только потом:

```sh
rm -rf "$RUN_DIR"
```

Не трогай рабочие PostgreSQL, MinIO, `.env`, dependency caches и listener'ы на
`8080`, `5173`, `55432`. Сервис не должен остаться запущенным после cleanup.

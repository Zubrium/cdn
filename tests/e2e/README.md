# Website/Router live E2E harness

Этот каталог — standalone Playwright-harness для живых сценариев публикации и
retirement. Он не входит в основной pnpm workspace и устанавливается отдельно:

```sh
cd tests/e2e
npm ci
GOWORK=off go vet ./...
```

Основной runner — `tests/qa/run-website-router-e2e.sh`: он копирует чекаут,
собирает и запускает продукт в изолированном окружении, проверяет target и только
затем запускает сценарии через `GOWORK=off go test ./...` в этом каталоге.
Ledger-символы
`TestPublication/E2E-PUB-001` и `TestRetirement/E2E-RET-001` определены в
`e2e_shim_test.go`; каждый из них запускает отдельный Playwright spec и требует
точную строку `PASS <scenario id>`.

`t.Skip`, fake-only и zero-test success запрещены; при RED runner обязан
завершиться ненулевым кодом и отсутствием точной PASS-строки для упавшего
сценария.

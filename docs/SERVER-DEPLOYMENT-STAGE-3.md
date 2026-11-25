# Server Deployment — этап 3: Deployment Automation

25 ноября 2026. Реализация и проверки выполнены локально на Mac. **К VPS не подключались.** Caddy, сертификаты, рабочий Mac-процесс, бинарник и пользовательские данные не изменялись. Реальные установка и перенос требуют следующего разрешённого этапа.

## Реализованные изменения

| Область | Результат |
|---|---|
| Команды с Mac | Makefile с help по умолчанию; явный ENV=production; отдельная INSTANCE=acceptance; strict SSH host-key; локальный Linux release, manifest/SHA и запрет dirty release |
| Installer | Проверка Ubuntu 24.04/amd64, RAM, диска/inode, Caddy 2.11.9 и всех TLS policies; пользователи, каталоги, units; сохранение исходного Caddyfile. Без создания app data, перевыпуска cert и изменения SSH/UFW |
| Разделение прав | Разные budget и budget-acceptance, live 0700, release/admin только для root, готовые backup доступны web только на чтение; LoadCredential, systemd hardening и ограничения памяти |
| Caddy | Управляемый JSON из проверенной конфигурации, актуальный leaf registry во всех policies, сохранённый ACME storage, exact Host/fallback 404, admin API выключен, validate+restart, режимы closed/trial/open/test-page |
| Deployment | Общая блокировка, durable journal, protected pre-update, миграция на кандидате, явная live migration, local и Mac HTTPS readiness, граница открытия записей, current/previous, recovery/rollback |
| Перенос legacy | Экспорт только через отдельную копию с проверкой существующего flock/parent lease и неизменности исходных bytes. Новый installation ID относится клону; сервер наследует ID архива, проверки stage 2 не обходятся |
| Backup | Существующий CLI и controller status/stopped/stop/start; daily в 03:30 Москвы, Persistent; 7 daily/2 обычных pre-update/pins; фактическое наблюдение расписания; предупреждение после 26 часов; fetch+verify |
| Очистка | Подтверждение удаления по backup ID или строгому имени previous-data. Защита pending/rollback references и последней исправной копии; неизвестные данные сохраняются |
| PKI | Issue/renew только на Mac существующей зашифрованной CA; передача только публичного leaf; versioned имена; отзыв по fingerprint, закрытие соединений и проверка отказа/resumption; отдельный encrypted DMG |
| Диагностика | Почасовая проверка обслуживаемого TLS-сертификата, warning 48h/critical 24h; serial change отдельно от события renewal; ресурсы, схема, backup, журналы операций, сроки устройств, reboot и ошибки materializer |

Новых пользовательских функций, SQL-миграций и зависимостей Go нет. Schema остаётся 14. Backend меняется точечно: `admin legacy-export`, `admin backup delete`, предупреждение backup после 26 часов и исключение отменённых HTTP-запросов из ложных internal 500. Настоящие ошибки не скрываются.

## Файлы и интерфейсы

- `Makefile`; `scripts/ops/mac.py`, `legacy.py`, `pki.py` — Mac transport и интерактивные операции. Python 3.9+ и стандартная библиотека, без GNU coreutils на Mac.
- `scripts/ops/remote.py`, `common.py`, `bootstrap.py`, `caddy_policy.py` — установленный root-wrapper с фиксированными операциями, без произвольных shell/path аргументов.
- `deploy/systemd/*` — app, daily backup, schedule observer, TLS timer, Caddy drop-in и ограничения journald.
- `legacy_export.go`, `backup_delete.go`, изменения `admin.go`, `backup_cli.go`, `backup_repository.go`, `internal/planner/http.go` — CLI и диагностика.
- `tests/ops/test_operations.py`, `tests/ops/cli_integration.py`, `legacy_export_test.go`, `internal/planner/deployment_logging_test.go` — целевые проверки.
- [Эксплуатация и точные команды](SERVER-OPERATIONS.md), [VPS-приёмка](SERVER-ACCEPTANCE.md), [формат архива](BACKUP-RECOVERY.md), [план с сохранённым аудитом](SERVER-DEPLOYMENT-PLAN.md).

Новых Go environment-параметров нет. Сохраняются BUDGET_ENV/DATA_DIR/BACKUP_DIR/INSTALLATION_ID/ADDR/PUBLIC_ORIGIN/ALLOWED_HOSTS/PROXY_MODE/MIGRATIONS и systemd credentials. Unit дополнительно задаёт GOMEMLIMIT=256MiB, GOMAXPROCS=1, TZ=Europe/Moscow. Параметры Make: ENV, INSTANCE, DEVICE, ARCHIVE, BACKUP_ID, DECISION, LOCAL_DATA, NAME, KIND, ALLOW_CORRUPT_LIVE.

```text
budget admin legacy-export --source ABS --backup-dir ABS --installation-id ID --confirm-stopped
budget admin backup delete --backup-dir ABS --installation-id ID --backup-id ID --confirm
budget-admin OP production|acceptance [REQUEST_TOKEN]
budget-controller status|stopped|stop|start INSTALLATION_ID
```

Root-wrapper читает ограниченный request JSON из фиксированного приватного каталога budgetadmin; проверяет владельца, идентичность операции, форматы ID, symlink/hardlink. Release разрешается только внутри releases. Используется обычный sudo с интерактивным паролем. Никакого NOPASSWD или web-sudo. `version/ready` и команды stage 2 сохранены.

## Согласованность и прерывания

Общий operations flock сериализует deployment, backup и PKI/Caddy. Внутри используются прежние блокировки repo/installation. Controller не берёт outer lock, чтобы избежать взаимной блокировки. Неопределённое состояние systemd означает отказ, а не «остановлено».

Журнал сохраняется до изменения live. До открытия доступа выполняются protected snapshot, пробная forward migration, явная миграция live, переключение release, запуск, local readiness и Mac HTTPS-проверка. В режиме trial наружу доступен только защищённый `/api/ready`. После durable `opening` автоматический откат данных запрещён: пользователь мог уже записать изменения.

Явный rollback предупреждает о потере новых изменений и сначала сохраняет текущую protected копию. Потерянный ответ commit сверяется по request ID. Прерванная публикация release pointer проверяется перед продолжением. Initialization имеет собственный журнал; неполные данные сохраняются отдельно, а не удаляются. Повторный deploy уже активного release не перезаписывает previous.

Caddy строится из сохранённой конфигурации и **актуального** registry. Старый backup конфигурации не может вернуть отозванный leaf. Durable intent записывается перед отзывом; root ExecStartPre сверяет registry и все TLS policies при каждом запуске Caddy, включая reboot после прерывания. При ошибке отзыва доступ закрывается. Session tickets дополнительно отключены явно; исходники Caddy 2.11.9 также отключают их при client auth и проверяют `VerifyConnection` при resumption ([connpolicy.go](https://github.com/caddyserver/caddy/blob/v2.11.9/modules/caddytls/connpolicy.go)). Это не заменяет проверку реального TLS на VPS.

SSH/UFW/swap не перенастраиваются, Certbot не устанавливается. Caddy admin API выключен в управляемой конфигурации; используется полный restart, закрывающий существующие соединения. ACME storage/account не пересоздаются. Фактическое автоматическое продление после установки unit остаётся серверной проверкой.

## Результаты проверок

Журналы сохраняются в игнорируемом `artifacts/server-stage3/`.

| Проверка | Результат |
|---|---|
| Go `go test ./...` | PASS: main 49.520s, planner 75.878s, recurrence 3.418s |
| Go race, GOMAXPROCS=2, -p1, timeout 90m | PASS: main 650.341s, planner 1445.894s, recurrence 1.533s |
| Go vet | PASS |
| Frontend unit | 66/66 PASS |
| Полный Playwright на итоговом Go/frontend | 176/176 PASS, 16.0 min, без skip |
| Python ops contracts | 41/41 PASS |
| Native CLI integration | PASS: настоящий init, full backup/verify, candidate restore/migration, live migration, откат до открытия записей, сохранение previous-data и admin check. OS/proxy заменены адаптером |
| Linux amd64 cross-build | PASS, 15 544 480 bytes, embedded frontend, schema 14 |
| Source govulncheck | PASS, No vulnerabilities found |
| Binary govulncheck | PASS, No vulnerabilities found |
| Linux/systemd/Caddy runtime | НЕ ВЫПОЛНЯЛСЯ: Mac, Docker не разрешён, подключение к VPS запрещено |
| git diff --check | PASS |

Диагностический ELF: `build/releases/d67a07feff07-20261125T075430Z/budget`, SHA-256 `ff0d90687b3ffd4abd2c522c31feca04826f5b485343d7e41ad7e898082ea290`. Он имеет `modified=true` и **не пригоден для production deployment**. После проверки и фиксации кода нужна новая сборка. Commit/push без согласования не выполнялись.

Ops-тесты покрывают идемпотентный installer, SSH/host-key failures, dirty/hash gate, нехватку диска/inode, symlink/hardlink, конкурентный flock, неправильный ID, все TLS policies/fallback/issuer, отказ применения отзыва, controller, фактическое расписание timer, ошибки на семи стадиях deployment, частичную initialization, потерю commit reply, ошибку timer после opening, запрет автоматического rollback после записей и прерванный release pointer.

Новые Go-тесты проверяют legacy data и fingerprints таблиц, неизменность исходных bytes, отказ при занятом Store, защиту pins/последней копии при удалении, границу 26 часов и cancellation logging. Результаты mock-тестов не выдаются за проверку Unix-прав, resource limits или TLS на Ubuntu.

## Ограничения и дальнейшая приёмка

1. Серверные операции не выполнялись. Linux, systemd, реальные права, Caddy JSON/headers/mTLS, Android download, reboot, нагрузка и renewal требуют [VPS-чек-листа](SERVER-ACCEPTANCE.md).
2. Installer закреплён на Caddy 2.11.9. Другая версия/неожиданная конфигурация требуют проверки, а не автоматического обновления.
3. Нужен чистый проверенный commit. В дереве находятся принятые предыдущие этапы и текущие изменения; автоматически они не фиксировались.
4. Synthetic acceptance использует отдельный loopback3002. Перед production import её нужно остановить; постоянная параллельная нагрузка двух приложений на864MiB не предполагается.
5. Действующие CA и ключи не читались и не менялись. PKCS#12 на втором Mac и PWA ещё требуют проверки. Отзыв с Mac probe требует сохранённой зашифрованной копии ключа отзываемого устройства; при её потере нужен отдельный административный сценарий закрытия доступа.
6. Pinned/manual/previous-data могут расти. Есть status и подтверждаемая cleanup; неизвестные остатки initialization сохраняются для разбора. При нехватке места операции отказывают, историю не удаляют.
7. Внешний backup не автоматизирован по решению пользователя. Без скачанного архива потеря VPS означает потерю данных. Секреты и актуальный PKI registry восстанавливаются отдельно.
8. Ограничения финансового last-write-wins и отсутствие push не меняются.

Точные первые команды находятся в начале [SERVER-OPERATIONS.md](SERVER-OPERATIONS.md). Следующий этап — отдельно разрешённое **синтетическое** развёртывание и приёмка. Реальная остановка Mac и перенос данных разрешаются отдельно после неё.

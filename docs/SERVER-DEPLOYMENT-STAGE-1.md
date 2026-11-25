# Server Deployment 1.0 — этап 1: Production Core

> Исторический отчёт этапа. Актуальный доступ: Caddy + публичный IPv4 HTTPS + обязательные mTLS и пароль. Tailscale/Serve не требуются; последующие решения — в [текущем плане](SERVER-DEPLOYMENT-PLAN.md) и [Backup & Recovery](SERVER-DEPLOYMENT-STAGE-2.md).

Дата: 24 ноября 2026. Базовый commit: `d67a07feff0772ba21182abfc5936496553bf26b`; изменения этапа находятся в рабочем дереве. Это подготовка приложения, а не установка на VPS.

## Результат и границы

Добавлены встроенный frontend, отдельный проверяемый production-режим, HTTPS Origin для локального reverse proxy, обязательная авторизация, ограничение финансовых запросов, защита тестовых путей и административные команды. Обычный production-запуск не создаёт отсутствующую базу и не применяет миграции.

К VPS не подключались. Работающий локальный процесс, `./budget`, пользовательские SQLite и финансовые JSON не заменялись; миграции к ним не применялись. Проверки нового приложения выполнялись на синтетических временных установках. Исходники функций финансового расчёта, планировщика и существующие SQL-миграции не изменялись.

Это не разрешение на production-развёртывание: Backup & Recovery и Deployment Automation остаются отдельными этапами. Не реализованы ежедневный backup, полный архив, новые кнопки скачивания, восстановление повреждённой установки, systemd units, SSH-доставка и rollback-скрипты.

## Встроенные ресурсы и Linux release

`static.go` встраивает **явно перечисленные 30 HTML/CSS/JS-файлов**, уже разрешённых HTTP allowlist. Каталог целиком не встраивается. Встроенные SQL, SVG, производственные календари и tzdata сохранены. Пользовательские категории, финансовые JSON, SQLite, `.git`, Node.js, исходники и секреты в release не включаются.

```sh
bash scripts/build-linux.sh
```

Команда создаёт новый `build/releases/<revision>-<UTC timestamp>/`, содержащий только `budget` и `release.json`. Используются Go 1.27.4, `linux/amd64`, `GOAMD64=v1`, `CGO_ENABLED=0`, trimpath и удаление отладочных символов. Manifest содержит revision, признак изменённых исходников, версию схемы, размер и SHA-256 **фактически собранного** файла. Скрипт проверяет VCS revision и отказывается работать через symlink каталога build. Рабочий `./budget` не затрагивается. Артефакты исключены из Git. Проверенный результат: `build/releases/d67a07feff07-20261124T162933Z/budget`, 15118496 байт (14.42 MiB), статический ELF amd64; SHA-256 `cdbb8aaaa7433a57dd1de72024affa6404a5097ac41c8128102ceef2df66889e`. В manifest честно отмечен `modified: true`, поскольку изменения ещё не закоммичены.

Release builder задаёт `-X main.releaseProfile=production`: запуск такого бинарника без явного `BUDGET_ENV` отказывается ещё до listener и открытия данных. Это проверено отдельным процессом из пустого cwd с проверкой причины отказа и отсутствия созданных файлов. Для обычного локального `go build` dev-default сохранён.

Ресурсы по умолчанию встроены и в локальную сборку. Внешний frontend разрешён только при одновременных явных `BUDGET_ENV=development` и `BUDGET_STATIC_DIR=/absolute/path/to/static`. Сохраняются allowlist, запреты traversal/ссылок и security headers.

## Конфигурация production

Пример **будущей** установки; команды запуска на рабочем приложении не выполнялись:

```text
BUDGET_ENV=production
BUDGET_ADDR=127.0.0.1:3001
BUDGET_DATA_DIR=/var/lib/simple-budget
BUDGET_INSTALLATION_ID=<32 hex из installation.json>
BUDGET_MIGRATIONS=check-only
BUDGET_PROXY_MODE=loopback-https
BUDGET_PUBLIC_ORIGIN=https://<node>.<tailnet>.ts.net
BUDGET_ALLOWED_HOSTS=<node>.<tailnet>.ts.net
BUDGET_PASSWORD_FILE=/etc/simple-budget/app-password
TZ=Europe/Moscow
```

| Параметр | Контракт |
|---|---|
| `BUDGET_ENV` | `development`, `test`, `production`. Без значения dev-режим остаётся только у обычной локальной сборки; release требует явного значения, production всегда указывается явно и его marker несовместим с dev/test |
| `BUDGET_DATA_DIR` | В production обязателен существующий абсолютный каталог с owner-only permissions, без пользовательских symlink |
| `BUDGET_INSTALLATION_ID` | Должен совпасть с маркером именно этого каталога; случайный неверный каталог не принимается |
| `BUDGET_MIGRATIONS` | В production строго `check-only`; применение миграций только отдельной командой |
| `BUDGET_ADDR` | По умолчанию `127.0.0.1:3001`; production допускает только loopback |
| `BUDGET_PROXY_MODE` | `loopback-https`: локальный HTTP upstream, внешний канонический HTTPS, без одновременного native TLS |
| `BUDGET_PUBLIC_ORIGIN` | Канонический HTTPS origin без пути, userinfo, query и fragment |
| `BUDGET_ALLOWED_HOSTS` | В proxy-режиме ровно один точный host[:port] канонического origin, без wildcard |
| `BUDGET_PASSWORD_FILE` | Абсолютный путь к regular-файлу без symlink/hardlink, owner-only permissions, пароль до 4096 байт UTF-8 плюс необязательное LF/CRLF; минимум 12 символов, рекомендуется 16 (уточнение политики перед VPS acceptance) |
| `CREDENTIALS_DIRECTORY` | Если password file не задан, читается `app-password` из systemd credential directory; Linux поддерживает точный read-only ACL root + UID сервиса (исправление VPS acceptance), без доступа группы/other |
| `BUDGET_PASSWORD` | Остаётся для разработки; в production одного env-пароля недостаточно, нужен file/credential. Одновременные file и env запрещены |
| `BUDGET_STATIC_DIR` | Только явно заданный development; запрещён в production/test |
| `BUDGET_TLS_CERT/KEY` | Прежний direct TLS остаётся для совместимости вне production proxy-профиля |

Для systemd этапа 3 предусмотрено `LoadCredential=app-password:/etc/simple-budget/app-password` вместо передачи секрета через environment/аргументы. Файл и секреты не входят в release/будущий пользовательский backup. Production принудительно выбирает `Europe/Moscow` для `time.Local`; tzdata встроена. UTC на самом VPS не является блокером.

Маркер `installation.json`:

```json
{"version":1,"environment":"production","installation_id":"<32 hex>"}
```

Проверяются все предки пути: нельзя обойти marker выбором вложенного каталога. Dev/test не открывают production marker; production не принимает test marker. До открытия SQLite проверяются путь и тип файлов, hardlink/symlink для DB/WAL/SHM/lock. Обязательные `planner.db`, `data.json`, `categories.json`, marker должны существовать и иметь закрытые права. Если JSON отсутствует, production не подставляет пустое хранилище.

Marker — защита от ошибок оператора, а не от злоумышленника с правами владельца файлов. Изоляция владельцев/systemd на следующем этапе обязательна. Смена файлов конкурентным привилегированным процессом не входит в эту модель защиты.

## Авторизация, reverse proxy и HTTP

Все маршруты, включая HTML/JS/CSS, финансовые и planner API, существующий экспорт и `/api/ready`, проходят общий middleware. Login — `budget`; пароль обязателен и на localhost. Basic credentials проверяются независимо от forwarded-заголовков. HTTPS завершается в Serve, Go слушает loopback HTTP. Порт приложения нельзя публиковать наружу.

Исправлена исходная ошибка: браузерный HTTPS Origin сравнивается с **настроенным каноническим origin**, а не с HTTP-схемой upstream. `Forwarded`, `X-Forwarded-Host`, `X-Forwarded-Proto` не устанавливают доверие и не меняют разрешённый Host/Origin. Проверки `Sec-Fetch-Site`, Content-Type для мутаций и Host сохранены. Отсутствующий Origin допускается для административного HTTP-клиента, но не отменяет пароль, Host и JSON Content-Type.

Проверен настоящий локальный HTTP reverse proxy через `httputil.NewSingleHostReverseProxy`: корректный HTTPS Origin принят; чужой Origin/Host, HTTP вместо HTTPS, `null` и попытка обхода через forwarded headers отклонены. **Фактический установленный Tailscale Serve ещё не проверен**: это обязательная серверная приёмка этапа 3, включая сохранение Host upstream.

Ограничения:

- Финансовые изменяющие `/api/` запросы: максимум **1 MiB**. Проверяются и Content-Length, и chunked-тело до входа в обработчик; корректный JSON с чрезмерным хвостом не успевает изменить данные.
- Одновременно буферизуются максимум **4** финансовых мутации; дополнительные получают 503. Это ограничивает память буферов примерно 4 MiB плюс overhead, не всю память процесса.
- Неудачные предъявленные credentials: общий ограниченный bucket, burst 10 и восстановление 2/сек; превышение возвращает 429 и `Retry-After: 1`. Не хранится неограниченная таблица IP/паролей. Корректные credentials не блокируются исчерпанным bucket, что важно при одном loopback peer Serve. Первичный запрос без Authorization получает 401 challenge и не расходует бюджет: иначе браузер с правильным паролем мог не получить возможность повторить запрос.
- Это ограничение ответа на неудачные попытки, а не длительный account lockout. Проверка правильного пароля остаётся возможной во время ограничения; основной барьер — приватный tailnet и сильный случайный пароль. Для публичной сети такая конфигурация не предлагается.
- Сохранены HTTP timeouts: headers 5 s, read/write 30 s, idle 60 s, max headers 1 MiB, graceful shutdown 35 s.
- Сохранены CSP, nosniff, DENY, no-referrer, no-store. Технические ошибки planner/finance логируются без тела запроса, содержимого задач, пароля и финансовых данных. Runtime production не печатает подробный raw error; административные ошибки сообщают причину отказа без секретов.

## Миграции и административные entrypoints

Новых миграций нет; текущая схема — **14**. Старые миграции не редактировались.

```sh
/path/to/budget version
/path/to/budget ready
/path/to/budget admin init --data-dir /absolute/empty-private-dir --confirm
/path/to/budget admin check --data-dir /absolute/data --installation-id <id>
/path/to/budget admin migrate --data-dir /absolute/data --installation-id <id> --confirm
```

- `version` не читает пользовательское хранилище. Возвращает JSON с Go/revision/dirty/schema/embedded_frontend.
- `ready` использует конфигурацию и пароль, обращается к **работающему** loopback HTTP-процессу с правильным Host, без environment proxy и redirects. Проверяет app/schema/revision/dirty/Go. `/api/ready` проверяет существующее соединение SQLite; второй Store не открывается, worker не запускается. Проверка не заменяет глубокий backup integrity-check. CLI для прежнего native TLS-профиля не реализован; остаётся авторизованный HTTPS endpoint.
- `admin init` — только осознанное создание **новой пустой** установки в уже созданном private-каталоге. Создаёт SQLite, пустые финансовые JSON и marker последним; существующие файлы не усыновляет/не перезаписывает. После частичного сбоя каталог не будет принят как готовая установка. Это не команда переноса рабочих данных.
- `admin check` открывает существующую БД под той же эксклюзивной файловой блокировкой, проверяет application ID, схему, ledger/checksums, integrity, foreign keys и ресурсы. Не мигрирует, не seed-ит, не запускает materializer. Это maintenance-доступ к SQLite, не immutable forensic mount: возможны служебные действия SQLite с journal/sidecar.
- `admin migrate` требует marker/ID и `--confirm`, применяет существующие транзакционные миграции и seed. Перед применением оператор обязан создать проверенный backup; автоматизация этого предусловия будет на этапе 2/3. Команда не предназначена для запуска поверх работающего сервиса.
- Обычный production startup требует точную текущую схему и WAL, проверяет ledger и целостность; старую/новую/пустую/чужую БД отклоняет. Синтетическая схема v13 была отклонена без изменения DB, затем успешно обновлена только `admin migrate`.
- Конкурентное maintenance-открытие при работающем сервисе отвергается flock. Закрытые права production-каталога и marker проверяются общей защитой также для административных команд, а не только при HTTP startup. Возврат старого бинарника после миграции не равен откату данных; восстановление проверенного совместимого снимка — следующий этап.

## Изоляция тестов

`internal/storageguard` используется до открытия Store и файлов. В Go test-процессе разрешены только реальные пути внутри системного tempdir, не сам tempdir. Проверяются marker предков и ссылки. `TestMain` отказывается работать при production environment, systemd credentials и опасных переопределениях тестовых путей. Подмена `TMPDIR` производственным каталогом также отклоняется.

Playwright и capture/review runners используют общий `tests/test-storage.cjs`: случайный private tempdir с **test marker**, канонизация, запрет production marker и файловых ссылок, очистка унаследованной production-конфигурации. Передаваемый вручную каталог обязан уже иметь корректный test marker. Go unit-фикстуры в `t.TempDir` не обязаны создавать marker, но проходят ограничения временного пути и запрет production marker. Положительный production lifecycle тест выполняется отдельным собранным процессом на синтетических данных; тестовый Store никогда не открывает production-данные.

Opt-in beta31 диагностика разрешает лишь явно подготовленный обычный snapshot без production marker и WAL/SHM, с копированием в tempdir; рабочую production БД указать нельзя. Performance report также разрешён только в безопасном временном каталоге.

Тестирование производственной установки — защита от ошибочных команд и путей, не sandbox для произвольного вредоносного тестового кода. На VPS не устанавливаются Node/Playwright, Git и инструменты разработки. Дополнительно этап 3 должен разделить OS-владельцев и рабочие каталоги.

## Зависимости и Git

Закреплён поддерживаемый Go **1.27.4**, `go 1.27.0`. По текущей политике Go поддерживаются две последние major-ветки; прежний локальный Go 1.25.5 для server release не оставлен. [Политика и releases Go](https://go.dev/doc/devel/release#policy).

Сканирование исходного Linux-бинарника обнаружило уязвимости `golang.org/x/image v0.32.0`. Применимы к загрузке пользовательского WebP: [GO-2026-6222 — excessive memory VP8L](https://pkg.go.dev/vuln/GO-2026-6222) и [GO-2026-5061 — WebP dimension panic](https://pkg.go.dev/vuln/GO-2026-5061). Ограничение входного файла/размеров не заменяет исправление декодера. [GO-2026-4961](https://pkg.go.dev/vuln/GO-2026-4961) относится к 32-bit и не целевой amd64. Остальные сообщения первого бинарного сканирования относились к TIFF/BMP/SFNT, которые приложение не импортирует; это не доказательство достижимости этих путей.

Целевое обновление: `x/image` **0.45.0**, необходимый ему indirect `x/sys` **0.47.0**. Остальные зависимости не обновлялись массово. После исправления binary-mode govulncheck не обнаружил известных уязвимостей.

`govulncheck` v1.10.0 в source-mode в этом окружении не смог разобрать синтаксис стандартной библиотеки Go 1.27 (ошибка type checker про generic methods). Поэтому source-mode **не отмечен успешным**. Выполнен поддерживаемый binary-mode по фактическому release; его результат не заменяется `go mod verify`. Следует повторить source-mode после совместимого обновления инструмента.

История Git с прежним `categories.json` не переписывалась, force push/gc/prune не выполнялись. Release строится из explicit embed и кода, без `.git` и личных JSON. `.gitignore` дополнен marker и будущим локальным production.env; build и тестовые артефакты уже исключены.

## Проверки

| Проверка | Результат |
|---|---|
| `go test ./...` | PASS на итоговом коде: root 19.494 s, planner 98.756 s, recurrence cache (предыдущий запуск 6.012 s) |
| `go test -race -json -count=1 -timeout=60m ./...` | PASS: root 76.464 s, planner 2105.211 s, recurrence 5.012 s; гонок не обнаружено |
| Повторный `go test -race -count=1 -timeout=10m .` после последних проверок release-profile/прав каталога | PASS: 48.761 s, включая production/HTTP/test-isolation проверки |
| `go vet ./...` | PASS |
| `npm test` | PASS: 62/62, включая 3 новых теста изоляции |
| `go mod verify` | PASS |
| Linux release build | PASS: статический ELF amd64, embedded frontend |
| `govulncheck -mode binary -show verbose <release>` | PASS: известных уязвимостей не найдено |
| `govulncheck ./...` (source mode) | BLOCKED: несовместимость type checker инструмента с Go 1.27 |
| `git diff --check` | PASS |
| Полный `npm run test:browser` | 142 PASS / 25 FAIL, 39.3 min; не зелёный, подробности ниже |
| Повторный Playwright на окончательной сборке: reliability + Beta 3.2 + planner | PASS: 26/26, 1.9 min |
| Повторный Playwright: оформление и пользовательские PNG/WebP | PASS: 8/8, 49.1 s, включая responsive widths |

Полный race завершён успешно. Для длительного SQLite-прогона выбран timeout 60 min; planner занял около 35 min. После заключительных уточнений проверки release-профиля и прав maintenance-каталога повторно прошли все обычные Go-тесты и весь root-пакет с race. Два существующих opt-in теста не запускались: `TestBeta31PrivateSnapshot` (не задана пользовательская копия, реальные данные в этом этапе не используются) и `TestFinalReviewServer` (это служебный runner отдельного визуального аудита). Новых skip не добавлено. Проверены production-конфигурация, credentials, marker/symlink защита, реальный proxy, обязательная авторизация, chunked/declared body limit, embedded allowlist, запуск из пустого cwd, readiness, maintenance lock и отказ автоматической миграции.

Полный браузерный набор содержит устаревшие ожидания прежнего интерфейса Beta. Четыре случая дополнительно запущены на неизменённом `git archive HEAD` в отдельном каталоге и порту: `acceptance-flow` ожидает закрытого диалога после настройки повторения, `acceptance-visual 1440` ищет старую кнопку «Изменить правило». Дополнительно до Production Core воспроизводятся расхождение даты в drag-and-drop тесте `ux-actions` (ожидается 03.12, остаётся 02.12) и высота первой задачи проекта в `ux22` (514.7 px вместо предела 500 px). Все четыре падают и на исходной версии. Это не означает, что поведение drag-and-drop корректно: оно остаётся отдельным продуктовым/тестовым дефектом, не исправленным этим инфраструктурным этапом. Проверки не удалены и неожиданные pageerror не разрешены. Отдельно исправлена выявленная новой защитой регрессия Basic challenge: запрос без Authorization всегда получает 401, даже после неверных паролей других клиентов. После исправления `acceptance-reliability` повторно прошёл: авторизация, финансовые операции, несколько настоящих перезапусков, идемпотентность, удаление, integrity/foreign_key_check. Первый полный прогон выполнялся до этой последней поправки; итог 142/25 не переименовывается в 143/24 без нового полного прогона. На окончательной сборке дополнительно прошли все 8 актуальных Beta 3.2 и 17 основных planner-сценариев.

Ошибки первого полного браузерного прогона и их итоговый статус:

| Группа | Количество | Наблюдение |
|---|---:|---|
| acceptance-flow | 1 | Ожидание закрытого диалога, исходная версия также падает |
| acceptance-reliability | 1 | Регрессия нового throttling исправлена, повторный запуск PASS |
| acceptance-visual | 5 | Ожидание старой кнопки «Изменить правило»; 1440 проверен на исходной версии |
| beta-dynamic | 2 | Timeout старого сценария настройки проекта |
| beta2 | 6 | Timeout сценариев отключения/массового повторения/проектов |
| ux-actions | 1 | Дата после drag-and-drop не изменилась как ожидает тест; подтверждено до этапа |
| ux-entity-templates | 5 | Timeout старых элементов управления повторениями/шаблонами |
| ux-navigation | 1 | Старый сценарий навигации по регулярным задачам и шаблонам |
| ux21 | 1 | Не найден прежний блок предпросмотра `.pl-info` с «Каждые» |
| ux22 | 1 | Геометрия 514.7 px против 500 px; подтверждено до этапа |
| zz-acceptance-load | 1 | После fixture 100 проектов/1000 задач не дождался видимой `.pl-task` за 5 s; причина не доказана, нельзя считать нагрузочную приёмку успешной |

Для не воспроизведённых отдельно baseline-случаев предполагаемая устарелость локаторов не выдаётся за доказанный диагноз. Не связанная с Production Core продуктовая логика не исправлялась ради зелёного результата. Логи и синтетические failure-contexts сохранены в игнорируемом `artifacts/production-core/checks/`. Перед серверной приёмкой нужен отдельный разбор оставшихся 24 сценариев; полный green browser gate пока не достигнут.

**Linux runtime не проверен:** на Mac доступен Docker CLI, но daemon не запущен; отдельного Linux runtime/VM в окружении нет. Рабочие приложения/контейнеры для этого не запускались, к VPS не подключались. Кросс-сборка ELF — проверка сборки, не Linux flock/runtime/systemd/Tailscale Serve. Их проверка обязательна при последующей серверной приёмке.

## Изменённые файлы

- `static.go`, `server.go`, `config.go`, `http_protection.go`, `admin.go`: embedded, конфигурация, HTTP защита и команды.
- `internal/storageguard/guard.go`, `internal/planner/store.go`, `storage.go`: маркеры, безопасные пути, startup/migrations, финансовое I/O и логирование.
- `internal/planner/http.go`, `internal/planner/materialize.go`: обезличенное техническое логирование.
- `production_test.go`, `environment_test.go`, `server_test.go`, `internal/planner/environment_test.go`: регрессионные проверки и test guards.
- `internal/planner/acceptance_test.go`, `beta31_test.go`, `final_review_server_test.go`: безопасные вспомогательные runners/пути.
- `tests/test-storage.cjs`, `tests/test-storage.test.cjs`, `tests/browser-server.cjs`, `tests/browser/acceptance-reliability.spec.cjs`, `tests/final-review/server.cjs`: единая изоляция браузерных серверов.
- `playwright.config.cjs`, `playwright.final-review.config.cjs`, `tests/ux21-review/playwright.config.cjs`, `tests/ux22/playwright.config.cjs`: проверка каталогов до тестовых записей.
- `go.mod`, `go.sum`, `scripts/build-linux.sh`, `.gitignore`, `README.md`, этот отчёт.

Предыдущий `docs/SERVER-DEPLOYMENT-PLAN.md` сохранён как исходный аудит; его описание состояния «до» не переписывалось.

## Следующие этапы

**Backup & Recovery:** согласованный полный архив при короткой остановке, manifest и версии схемы, SQLite+финансовые JSON+пользовательские иконки в SQLite+несекретные настройки; checksum/integrity/restore drill; публикация только проверенного архива; ежедневный запуск 03:30 Europe/Moscow; последние 7 успешных ежедневных и до 2 обычных pre-update копий с pin для rollback; контроль свободного места; административные fresh backup/restore; авторизованные кнопки скачивания последнего проверенного planner/full архива с датой/размером. Каталог backup вне static; финансовая кнопка остаётся. Секреты/ключи в backup не включаются.

**Deployment Automation:** отдельный OS-пользователь, systemd credentials/hardening, loopback+Serve, установка/перенос/обновление/rollback/status/logs с Mac, backup-before-update, запуск явной миграции при остановленном приложении, readiness и возврат совместимого snapshot при неуспехе. Только current/previous release, ротация журналов и проверка места. Для доставки требовать чистый зафиксированный commit: `revision + modified=true` не различает две незакоммиченные сборки; manifest SHA-256 различает, но readiness не вычисляет hash собственного бинарника. Swap уже есть (1.7 GiB), дополнительный не нужен. Учитывать фактические 865 MiB RAM и 9.3 GiB свободного диска, измерить RSS/startup/materializer на Linux; не обещать запас RAM только по размеру бинарника.

Без внешнего автоматического backup полная потеря VPS уничтожит и локальные копии: внешнюю копию пользователь должен скачивать на Mac. Принятые ограничения финансового last-write-wins и отсутствия push-синхронизации сохраняются.

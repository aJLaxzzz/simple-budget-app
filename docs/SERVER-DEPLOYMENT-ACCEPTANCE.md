# VPS Deployment & Acceptance — журнал приёмки

Дата начала: 25 ноября 2026. **Синтетическая серверная приёмка принята пользователем с документированными ограничениями при постановке Server UX 1.1.** Перенос настоящих данных отложен до актуальных финансовых JSON и категорий и отдельно не разрешён. Acceptance продолжает работать. Первый автоматический daily и фактическое automatic HTTPS renewal остаются отдельными ожидаемыми проверками, не отмечаются пройденными. Ниже сохранена хронология; ранние pending-статусы относятся к соответствующим этапам. Ограничения покрытия сохраняются; принятие синтетического этапа не является подтверждением полной production-приёмки.

## Предрелизная проверка

Проверены tracked/untracked файлы, staged/unstaged diff, .gitignore и все embed-директивы. Просмотрены 95 изменённых исходников, тестов, units и документов принятых этапов. Среди 629 доступных Git-кандидатов нет личных SQLite, пользовательских data/categories JSON, ключей, сертификатных контейнеров, credentials, backup и бинарников. Private-key headers не найдены. JSON в Git — package metadata, встроенные производственные календари, каталог иконок и тестовая fixture.

`categories.json` присутствовал в старой истории (включая c30f105/fdf2dbc), но отсутствует в текущем HEAD/index. Рабочий файл не читался и не удалялся, повторно не фиксировался. История не переписывалась; push/force push не выполнялись.

Локальные commits:

- `d6fa5fd9998bbda1242653bd4032ea766b043585` — принятые Production Core, Backup & Recovery и Deployment Automation, 95 проверенных файлов. Добавлялись по проверенному списку с SHA-256, не через `git add -A`.
- `59ad389f190ebd4f6294077128904177f8a1917d` — отдельная диагностика VPS без изменения конфигурации; добавлен тест запрета изменяющих команд и чтения закрытых ключей.

Production release подготовлен, **ещё не установлен**:

```text
commit: 59ad389f190ebd4f6294077128904177f8a1917d
path: build/releases/59ad389f190e-20261125T084453Z/budget
SHA-256: eca6d3a7043cd69b268033ea695095b05c973093f2ea11fb4f34cc6e0009db5b
size: 15544480 bytes
modified: false
schema: 14
platform: linux/amd64
embedded_frontend: true
```

Каталог release содержит только binary и manifest. Embed allowlist содержит frontend, встроенные иконки, 14 миграций и производственные календари; личные файлы не включены. Binary govulncheck для указанного ELF: PASS, No vulnerabilities found. Ранее проверенная первая чистая сборка d6fa5fd также сохранена для последующей проверки двух release.

Повторно перед commit: Go PASS (main34.500s, planner50.413s), vet PASS, frontend66/66, ops41/41. После добавления preflight — ops42/42 PASS. Код приложения не менялся относительно принятого этапа3: прежние race PASS и полный Playwright176/176 относятся к тем же Go/frontend исходникам; заново полный browser/race на этом шаге не запускались. `git diff --check` PASS.

## Реально выполнено на VPS и через публичный HTTPS

SSH alias budget разрешается в budgetadmin@171.22.237.16. Пользователь предоставил совпадающий результат проверки публичного ED25519-ключа в ответ на запрос независимой сверки:

```text
SHA256:N+hLwBbvvqlQxsk/jFBsb2hZrhIcmLzueZzHPXCX3OI
```

Соединение выполнено с `StrictHostKeyChecking=yes`, `HostKeyAlgorithms=ssh-ed25519`, `UpdateHostKeys=no`, без запроса пароля через инструменты агента.

Фактический baseline 25.11.2026 08:42UTC:

| Параметр | Результат |
|---|---|
| ОС | Ubuntu24.04.5 LTS, VERSION_ID24.04 — отличается только point release от исходного описания |
| Архитектура | x86_64 |
| RAM | 906964992 bytes; available598220800 bytes, около570MiB |
| Swap | 1836052480 bytes, использовано274432 bytes |
| Диск | 15741165568 bytes, доступно8954011648 bytes, около8.34GiB |
| Inode | свободно871040 из983040 |
| Load | 0.00 / 0.01 / 0.00 |
| Caddy | v2.11.9, active/running/enabled |
| Tailscale | tailscaled.service not-found/inactive |
| App units | production и acceptance ещё отсутствуют |
| Listening TCP | 22,80,443; admin2019 только127.0.0.1; 3001/3002 отсутствуют |
| sudo без интерактивного ввода | отказ `a password is required`; обход не применялся |

Проверка HTTPS с Mac (08:45UTC), с верификацией публичной цепочки и IP:

- TLS1.3; issuer Let's Encrypt YE1.
- Serial `06C2AC4FA4A90AB446B1395D4B0F149D85FA`, SAN IP171.22.237.16.
- NotBefore24.11.2026 20:46:11UTC; NotAfter01.12.2026 12:46:10UTC.
- Без клиентского удостоверения HTTP не получен: TLS alert `certificate required`.
- Сертификат не перевыпускался, системное время не менялось. Первое автоматическое renewal остаётся pending до реального события; ориентир проверки пользователя —29ноября.

Привилегированный preflight выполнен пользователем 25.11.2026 в08:56:22UTC; результат независимо скачан с VPS. Изменений конфигурации не применялось:

- Caddyfile SHA-256 `5f3f31549b706454781dbbddb40c41243e79276a3eeba5f86a5bd1ede8a4c3a4`.
- Проверены обе TLS policies: require_and_verify, правильная CA и точный одинаковый allowlist mac-1/phone. ACME storage сохранён: `/var/lib/caddy/ip-acme-production`.
- UFW active, default deny incoming, allow outgoing; открыты только TCP22/80/443 дляIPv4/IPv6. UDP443 не слушается; admin2019 — только loopback.
- Оба клиентских удостоверения действуют до24.11.2027. Закрытые ключи не читались; проверялись публичные CRT.
- Незавершённых journal/installation paths нет. App units и данные ещё не созданы.
- Caddy MemoryCurrent11014144bytes (около10.5MiB) — это потребление cgroup, **не замер RSS процесса**.
- UTC/NTP подтверждены отдельным `timedatectl show --property=Timezone --property=NTPSynchronized`: Etc/UTC, yes. Исправлен пустой вывод clock в preflight: timedatectl требует раздельные property аргументы.
- `session_tickets_explicitly_disabled=false` описывает отсутствие явной настройки в исходном JSON, а не результат испытания resumption. В managed JSON будет explicit disabled; реальные TLS1.2/1.3 проверки после отзыва ещё pending.

### Исправление installer до применения

На VPS в отдельном временном unit, без запуска сервисов, подтверждено: `systemd-analyze verify` возвращает exit1 для ещё отсутствующего `/opt/simple-budget-acceptance/current/budget`. Это ломало первую установку до загрузки release; локальный mock ранее не воспроизводил проверку исполняемого файла.

Installer теперь проверяет структуру ещё не инициализированных app units на временных копиях с существующим бездействующим executable. **Установленные units не подменяются**. Controller перед каждым start обязательно выполняет полную verify оригинального unit с настоящим release. Для уже установленного release проверка полная и на этапе installer. Добавлены проверки неизменности оригинальных units и запрета start при ошибке настоящей verify. Диагностические timers включаются только после освобождения installer lock: OnBootSec мог уже пройти, и немедленный callback не должен конфликтовать с установкой. Локально: ops44/44 PASS, native CLI integration PASS; повторная установка проверяет доступность lock при включении timers. Go/frontend не менялись. Результат before/after пробы хранится в `artifacts/server-acceptance/unit-initialization-probe.json`.

### Отказ первого installer: системная ссылка os-release

После первого запуска `make server-install` подтверждён конкретный источник `Regular non-linked file required`: `/etc/os-release` — root-owned symlink `../usr/lib/os-release`. Цель `/usr/lib/os-release` — root-owned обычный файл0644, nlink1. Общий reader намеренно запрещает symlink; тестовый OS fixture раньше был обычным файлом и не воспроизводил устройство Ubuntu.

Фактическое состояние VPS после отказа:

- Есть `/var/lib/simple-budget-admin`, root:root0700. По порядку кода ошибка произошла после получения административного lock, до чтения Caddy и создания пользователей; содержимое закрытого каталога отдельно без sudo не просматривалось.
- Пользователей budget/budget-acceptance, app/release/backup каталогов обеих установок и установленных budget-admin/controller нет.
- App units и новые diagnostic timers отсутствуют; Caddy active/enabled, PID2943 — прежний.
- SHA-256 Caddyfile остался `5f3f31549b706454781dbbddb40c41243e79276a3eeba5f86a5bd1ede8a4c3a4`. Installer не дошёл до его сохранения/адаптации; SSH, UFW, сертификаты и данные не менялись.

Добавлен узкий `read_os_release`: допускается только обычный защищённый OS-файл либо точная системная ссылка `/etc/os-release` на `/usr/lib/os-release` (относительная стандартная форма или её точная абсолютная форма). Проверяются владелец, отсутствие записи для group/other и доверенные каталоги; цель читается непосредственно через существующий single-link/O_NOFOLLOW guard. Произвольный resolve/follow symlink не используется. Общие read/regular и проверки данных, credentials, release/backup не ослаблялись. Этот reader используется также в административном status, где была та же проблема.

Проверки исправления:

- На реальном VPS старый reader снова дал исходную ошибку, новый прочитал Ubuntu24.04.5. Это только чтение; изменения не применялись. Evidence: `artifacts/server-acceptance/os-release-regression-vps.json`.
- Ops49/49 PASS; native CLI integration PASS; py_compile и git diff --check PASS.
- Fixture installer теперь использует настоящую структуру Ubuntu со ссылкой. Воспроизводится первоначальный отказ после создания admin directory/lock, затем успешный запуск исправленного installer и повторный успешный запуск. Дополнительно проверено продолжение после искусственного прерывания уже после записи units.
- Отдельно проверены произвольная ссылка, цепочка ссылок, symlink каталога, hardlink источника/цели, неправильный владелец и group/world-writable файл/каталог — отказ. Общий reader по-прежнему отвергает даже допустимую системную ссылку.

Повторный installer выполнен пользователем успешно: `infrastructure_prepared`, `application_initialized=false`, `caddy_changed=false`, Caddy v2.11.9. Независимая read-only проверка: созданы budget/budget-acceptance и административные wrappers, app units loaded/inactive/disabled, 3001/3002 не слушаются. Diagnostic timers enabled/active; schedule service успешно сработал, TLS monitor на момент проверки ещё ожидал первого запуска. Caddy PID2943 и SHA-256 Caddyfile прежние. Evidence: `artifacts/server-acceptance/post-installer.json`.

## Уточнение политики пароля перед acceptance

По явному запросу пользователя минимум пароля приложения снижен с 32 до **12 символов**, рекомендация — **16**. Backend (env для разработки, защищённый password file и systemd credential) и административный `initialize` используют одинаковую нижнюю границу. Unicode считается по символам, верхняя граница — 4096 байт UTF-8; необязательный завершающий LF/CRLF файла не входит в длину пароля. Ранее принятые длинные пароли сохраняются. Административный ввод остаётся скрытым и требует повторения; секрет не передаётся в аргументах/env/логах. Общие проверки прав/ссылок, обязательные mTLS и Basic Auth, Host/Origin и ограничитель неверных попыток не отключались.

Добавлены проверки 11/12/16 символов, прежних длинных паролей, Unicode, верхней границы, ошибочного подтверждения и отсутствия записи credential/journal при отказе. Production lifecycle и реальный локальный HTTP proxy проходят с 12-символьным синтетическим паролем; native административная интеграция также создаёт 12-символьный credential. Исходный аудит в SERVER-DEPLOYMENT-PLAN сохранён без изменений; актуальная политика явно указана перед ним.

Проверки этой поправки: `go test ./...` PASS (основной пакет 44.408 s, planner/recurrence — cache), `go vet ./...` PASS; целевой race-прогон конфигурации, Basic Auth, proxy и rate limit PASS (2.668 s); ops **50/50**, native CLI integration PASS; frontend **66/66**; целевой Playwright `acceptance-reliability` **1/1** с 12-символьным паролем PASS. Source govulncheck PASS. Полный Playwright и полный race повторно для этой точечной поправки не запускались. OS/proxy native-интеграции остаются mock; реальные HTTP proxy и production binary lifecycle здесь проверены на Mac, не на VPS. `git diff --check` PASS. Release собирается отдельно после чистого commit, без замены рабочего `./budget`.

Установленные ранее VPS-скрипты пока содержат прежний минимум. Перед `initialize` обязательно повторить installer из нового чистого commit. Пересборка Go сама по себе root-wrapper не обновляет. Настоящие данные, локальный рабочий процесс и бинарник не менялись; acceptance в рамках этого изменения не инициализировалась.

## Что ещё не проверено

Installation ID отсутствует: synthetic installation ещё не создана. Linux ELF runtime, systemd/LoadCredential/WAL/flock, OS-права web/admin, managed Caddy, app authentication, GET/POST/download/Range, daily backup, restore/corruption, deployment/rollback/recovery, нагрузка100/1000 и пики памяти — **pending**.

Управление acceptance-device, HTTP2/keepalive и TLS1.2/1.3 resumption после отзыва, сохранение mac-1/phone, encrypted PKI backup — **pending**. Действующие ключи не читались, не изменялись и не передавались.

Mac/Android через Wi-Fi/mobile/VPN, reboot после отдельного согласования, первое реальное automatic renewal — **pending**. [Основной чек-лист](SERVER-ACCEPTANCE.md) пока не отмечается как пройденный. Прежние локальные тесты не заменяют серверные проверки.

## Следующий шаг пользователя

На Mac выполнить одну команду (пароль sudo вводить только в собственном терминале):

```sh
make -C "$HOME/Desktop/simple-budget-app" server-install ENV=production
```

Повторный installer обновит уже установленные административные скрипты и units, сохранив существующих пользователей и исходную Caddy-конфигурацию. Приложение и его SQLite ещё не инициализируются; Caddy не переключается на proxy. После проверки результата отдельно запускается initialize acceptance. Не запускать остальные команды непрерывным блоком.

На текущей точке система **ещё не готова к переносу настоящих данных**: серверная приёмка не завершена. Рабочий Mac-процесс PID38114 и бинарник остаются прежними; локальные финансовые JSON и SQLite не менялись.

Обезличенные журналы: `artifacts/server-acceptance/` (исключён из Git). Этот документ обновляется по результатам дальнейших шагов.


## Подтверждённый сбой LoadCredential и подготовленное исправление

Инициализация release 7ba39d5 на acceptance дошла до запуска: app exit1, три автоматических рестарта. Журнал: `password file must be private, regular and not a link`. Синтетическая transient-unit проба на настоящем VPS (systemd255.4) подтвердила root:root0440, nlink1, UID приложения995; ACL v2: USER_OBJ r--, USER995 r--, GROUP_OBJ ---, MASK r--, OTHER ---. Пароль приложения в диагностике не использовался. Общая owner-only проверка ошибочно трактовала ACL mask как доступ группы. Основание поведения: [systemd v255 exec-credential.c](https://github.com/systemd/systemd/blob/v255/src/core/exec-credential.c#L176-L192).

Добавлен отдельный Linux-reader только для CREDENTIALS_DIRECTORY: один открытый O_NOFOLLOW descriptor, regular/single-link/owner/size проверки и точный ограниченный ACL, без дополнительных пользователей/групп и записи. Обычный password file не получает исключение. ACL читается через уже закреплённый golang.org/x/sys v0.47.0; версия зависимости не менялась. Добавлена безопасная read-only команда `admin check-credential` для испытания настоящего LoadCredential без доступа к SQLite и без вывода секрета.

Для сохранения уже созданной synthetic установки подготовлен `make repair-initialization`: guards установки и незавершённой операции, проверка прежней схемы, реальная systemd credential-проба до переключения, protected backup, durable journal и release pointer. Он не пересоздаёт данные/пароль, не меняет Caddy и не снимает локальный pending. После результата выполняется обычный `make recover ... DECISION=finish` с HTTPS proof. См. операционную инструкцию. Повторный `initialize` не требуется.

На VPS уже реально выполнен отдельный Linux test executable от budgetadmin в временном каталоге: ACL parser по наблюдавшимся метаданным, Linux file/permission/symlink/hardlink guards, credential diagnostic без секрета, размеры/формат password file — PASS. Файл тестового бинарника удалён после завершения. Это не подменяет ещё не выполненную пробу нового ELF внутри LoadCredential: она встроена в repair-команду и требует интерактивного sudo. Evidence: `artifacts/server-acceptance/credential-linux-tests.log`.

Установка исправленного release и возобновление сервиса пока pending. Рабочая Mac-установка не затронута. Первое автоматическое renewal по-прежнему pending.


Проверки исправления ACL: `go test ./...` PASS (основной пакет37.545s; planner/recurrence cache), vet PASS, целевой race конфигурации/ACL/auth/proxy PASS2.465s; ops55/55, native CLI integration PASS; frontend66/66, целевой Playwright acceptance-reliability1/1 PASS9.1s, source govulncheck PASS, py_compile и diff-check PASS. Полный браузерный набор и полный race для этой поправки не повторялись. Новые тесты repair покрывают запрет production/чужого ID/другой операции/публикации/смены схемы, отказ credential-пробы без переключения, прерывание journal→pointer, сохранение данных/credential/pending и порядок probe→backup→switch. Реальное продолжение acceptance ожидает выполнения пользователем подготовленной команды.


## Успешный запуск и промежуточный HTTPS acceptance (25 ноября, 11:14–11:17 UTC)

- Установлен clean release `623319941eac5fadc48ef1143c3af7b8743ebbdc`, SHA-256 `4fb2703a1ad54a05ca18b0b9af158809a8be2f548932a2076064771df03de51e`, schema14. Installation ID синтетической установки: `6919a32cbb5e4cd09a2a00f691e32812`.
- Repair подтвердил настоящий systemd LoadCredential новым ELF (`credential_probe: passed`), создал protected backup `20261125T111330Z-7d9bd654ff6b9732`. Recover прошёл authenticated readiness и Mac HTTPS mTLS/password proof, вернул committed. Data restore не выполнялся.
- Read-only SSH: app и Caddy active/running, NRestarts0; app слушает только127.0.0.1:3002;3001 и2019 отсутствуют. RSS выборочно app18592KiB, Caddy47608KiB (не peak). Без app password все проверенные localhost API/static возвращают401; публичный TLS без client certificate отклонён TLSV13_ALERT_CERTIFICATE_REQUIRED.
- Daily timer next26.11.2026 00:30UTC =03:30МСК. TLS/schedule timers active и уже запускались; это не доказательство renewal. Серийный номер публичного сертификата пока прежний.
- HTTPS smoke дошёл до скачивания: успешно проверены корректная авторизация, неверный пароль, чужие Host/Origin, отсутствие client cert, создание/чтение synthetic project/task, ручной backup с остановкой/возобновлением app и чтение backup metadata. Backup `20261125T111716Z-6a2003d4c4009d94`, размер50595B, SHA256 `217ede041e1cc04eee073a92a98b939022dc7763e7a139c14fdbcb6e4c5c272a` проверен на VPS. Внешняя проверенная копия на Mac пока не получена.

Smoke остановился на объединённой проверке HTTP status и точного равенства Cache-Control строке `no-store`; старое сообщение скрывало различие. Изолированная проба тем же Caddy2.11.9 на VPS воспроизвела200 с **двумя** Cache-Control:no-store (proxy + upstream), которые Python HTTPResponse объединяет в `no-store, no-store`. Рабочая конфигурация Caddy не менялась; временный процесс слушал толькоloopback, использовал синтетический upstream и собственные временные каталоги, остановлен после проверки. Evidence: `artifacts/server-acceptance/caddy-header-probe.log`.

Исправлен только Mac acceptance runner: допускает повтор одного и того же no-store, регистр/пробелы; отсутствие no-store, public/no-cache/max-age и HTTP ошибки по-прежнему отвергаются. Дополнительно проверяет attachment. Ошибка теперь содержит HTTP status и Cache-Control без Authorization/пароля. Скачивание потоковое во временный файл, публикация после независимого archive verify и совпадения installation/backup ID, без перезаписи существующего файла. Добавлена `acceptance-download` для повторения только чтения конкретного уже созданного архива, без новых задач/backup/остановок/sudo.

Локально ops58/58 PASS, включая настоящий HTTPResponse с повторёнными заголовками, отрицательные варианты, ошибку проверки/идентичности и отсутствие публикации повреждённого архива. Native CLI integration PASS; py_compile/diff-check PASS. Go/frontend и VPS release для этого изменения не менялись; прежние полные результаты не выдаются за новый прогон. Фактическую причину именно последнего download response и успешность скачивания подтвердит повтор `acceptance-download`; весь smoke пока не помечен PASS. Range, restore/update/rollback, права, нагрузка, устройства, reboot и первое automatic renewal остаются отдельными gates.


## HTTPS download подтверждён

`acceptance-download` успешно скачал существующий `20261125T111716Z-6a2003d4c4009d94` без нового backup и без остановки приложения. Путь внешней копии: `~/.simple-budget-admin/acceptance-20261125T111716Z-6a2003d4c4009d94.tar.gz`. Отдельная проверка на Mac подтвердила точное совпадение SHA256 с сервером (`217ede041e1cc04eee073a92a98b939022dc7763e7a139c14fdbcb6e4c5c272a`), размера50595B, installation ID и schema14. SQLite integrity_check=ok, foreign_key_check пуст. В архиве один synthetic project и одна task; контрольные ID сохранены в игнорируемом `artifacts/server-acceptance/verified-http-download.json` для сопоставления после restore. Состав архива проверен штатным verify; исходные личные данные Mac не использовались.

Таким образом, GET/POST/auth/Host/Origin/backup-stop-start из первого smoke и исправленная проверка HTTP-download выполнены реально раздельными запусками. Прежний неуспешный запуск smoke не переименовывается в полный PASS; backup Range и остальные серверные gates ещё не проверены. Следующий шаг — штатное полное восстановление acceptance из этой согласованной копии с сохранением предшествующего поколения и новым HTTPS proof.


## Восстановление и recovery после потери SSH завершены

Restore архива `20261125T111716Z-6a2003d4c4009d94` дошёл до `awaiting_https_acceptance` (operation `7edb29aed7dd45e08a4c0d800043999d`), затем SSH/SCP оборвался при получении result.json, до Mac HTTPS proof. Серверный результат сохранился, новый restore не запускался. На Mac pending продолжал содержать исходный operation ID.

Диагностика показала активные app/Caddy/SSH, load0, available545MiB, отсутствие OOM в предоставленном фрагменте kernel journal. Внутри VPS TLS1.3, Verification OK, без client cert ожидаемый certificate required. С телефона защищённая страница Maintenance доступна. Маршрут Mac шёл через VPN utun9; после отключения VPN тот же recover прошёл HTTPS proof и вернул committed. Это связывает наблюдавшуюся нестабильность с маршрутом через VPN, но не устанавливает внутреннюю причину работы VPN. Пользователь добавил IP/CIDR bypass; отдельная проверка bypass ещё впереди. SSH/firewall/mTLS не ослаблялись.

После commit локальный pending отсутствует; независимый SSH подтверждает active/running app и Caddy, NRestarts0, current `623319941eac-4fb2703a1ad54a05`, daily timer03:30МСК. Повторного применения restore при finish не было. Сверка всех объектов/ID после восстановления и восстановление специально повреждённой synthetic DB пока не закрыты; успешный startup/commit не подменяет эти проверки.

Следующий административный шаг — update на второй чистый release, затем explicit rollback на6233199. До переноса настоящих данных также остаются права/WAL/flock, полная сверка данных, ресурсная нагрузка100/1000 и пики, revoke/resumption, PKI-копия, клиентские браузерные сценарии, reboot (после отдельного согласования), первый daily и фактический automatic renewal. Ни один pending-пункт не считается пройденным только по локальным тестам.


## Update: trial достигнут, HTTPS proof ещё не завершён

Deploy чистого release `379bc1cd1b33421d25f1183d4afb57342c079cf6`, SHA256 `f3bf6358d3e0979bdca95d3082805693cc133a7963de15c2351bc0bc166ba72e`, дошёл до awaiting_https_acceptance (operation df70cfa2e8554045b60b33bc26a1cba5). Независимый SSH подтвердил current новой версии, app/Caddy active, NRestarts0. Затем Mac получил HTTP-ответ не200 на `/api/ready`; прежнее сообщение `Protected HTTPS readiness failed` не сохраняло status, поэтому точная причина (401/403/503/другая) по выводу не установлена. Это отличается от предыдущего timeout SSH/TLS. Update пока не считается committed; повторный deploy правильно заблокирован pending.

Улучшена только локальная диагностика HTTPS proof: выводит HTTP status и безопасное пояснение401/403/429/502/503, не пишет пароль, Authorization или response body. Проверка200 и точного clean revision/schema сохранена. Ops60/60 PASS, включая отрицательные статусы, отсутствие секрета в ошибке и отказ при несовпадении версии/схемы. Серверный release/данные этой правкой не менялись. Продолжение — штатный recover finish того же operation ID; новый deploy не нужен.


## Update подтверждён; повтор ввода пароля

Пользователь предоставил результат продолжения operation `df70cfa2e8554045b60b33bc26a1cba5`: первый HTTPS proof вернул HTTP401, следующий с правильным паролем завершился `committed` для release `379bc1cd1b33-f3bf6358d3e0979b`; локальный runner завершил очистку incoming. Для этого отказа подтверждена ошибка авторизации приложения, а не сетевой timeout. Успешное обновление зафиксировано по выводу команды пользователя; explicit rollback ещё не проверен.

Mac HTTPS proof теперь допускает максимум три попытки ввода пароля приложения при HTTP401 в рамках одной команды. Повторяется только GET `/api/ready`, TLS context используется повторно: пароль клиентского ключа, sudo и серверная операция не повторяются из-за опечатки. HTTP403/429/502/503, сетевые ошибки и несовпадение release/schema по-прежнему останавливают проверку без автоматических повторов. После исчерпания попыток либо отмены pending остаётся для штатного recover; серверный rate limit, mTLS и Basic Auth не изменены.

Локально ops63/63 PASS и diff-check PASS. Тесты покрывают две опечатки с последующим успехом, предел трёх попыток, отсутствие повторов при других HTTP status/сетевых ошибках, отмену ввода и закрытие соединений. Эта поправка находится только в административном клиенте Mac и не требует обновления VPS. Интерактивный повтор с опечаткой на VPS пока отдельно не проверялся.


## Explicit rollback завершён (25 ноября 2026)

По выводу пользователя операция `4f9fc8b1ec6d46d7a80edefceca15ad2` прошла awaiting_https_acceptance и завершилась committed: release `623319941eac5fadc48ef1143c3af7b8743ebbdc`, SHA256 `4fb2703a1ad54a05ca18b0b9af158809a8be2f548932a2076064771df03de51e`, schema14, modified=false. Очистка incoming завершилась. Повторный ввод sudo был принят, операция не прервалась.

Независимая read-only проверка SSH после отката: app и Caddy active/running, NRestarts=0; current указывает на6233199, previous — на379bc1c. Daily timer `simple-budget-backup@acceptance.timer` запланирован на26ноября00:30UTC (03:30МСК), первого запуска ещё не было; TLS/schedule timers активны и запускались. Выполнены update и explicit rollback между двумя чистыми release на настоящем Linux. Детальная сверка восстановленных объектов, protected backup и предыдущих поколений остаётся отдельной проверкой; успешный committed её не заменяет.


Status пользователя после rollback (25ноября12:16UTC): deployment_journal=false, restore_journal=false, backup_operation_pending=false, revocation_pending=false; synthetic=true, service=running, proxy=open, configuration_matches=true. Latest verified backup `20261125T121114Z-9e88c86a1d0fff62` создан перед откатом с379bc1c, SHA256 `5e121a4d77f32279f211865ebf931936deaee6a3639722526d4e148240e6e49e`, размер50576B. Счётчики: daily0, pre-update4, manual2, pins4. Эти агрегаты не доказывают соответствие каждой копии конкретному pin; ротация и обоснованность удержания проверяются отдельно, автоматическая очистка не выполнялась. AvailableRAM538204KiB, свободный диск8872046592B, load0; ошибок materializer/internal/ACME в сводке0. TLS serial прежний, срок144.49h, renewal не подтверждён. Отсутствие события выпуска в текущей сводке не означает отсутствие действующего сертификата.


## Тестовое устройство: выпуск, добавление, отзыв

Пользователь выпустил `acceptance-device` на Mac существующим CA, проверка OpenSSL sslclient завершилась OK. Публичный сертификат (SHA256 `f7cf50cd0c4715f451644c55afd1864f84362297ceaeed85bbe5b616ce89f1b9`) добавлен25ноября12:19:18UTC и отозван12:59:03UTC. CLI вернул state=revoked, connections_terminated=true, device_probe=`old connection closed; new/resumption handshake rejected`. Свежий экспорт registry сохраняет mac-1 и phone allowed, acceptance-device revoked. Закрытые ключи не передавались на VPS.

Граница доказательства: существующий probe использует HTTP/1.1 keep-alive и TLS, выбранный библиотекой, проверяет отсутствие ticket у полученного сеанса и попытки нового соединения/соединения с сохранённым SSLSession. Вывод не содержит согласованной версии TLS, поэтому отдельные TLS1.2 и TLS1.3, HTTP/2 и полный жизненный цикл выдачи tickets не считаются проверенными этим запуском. Проверка закрытия старого канала также принимает OSError (включая timeout), поэтому её результат следует дополнить более строгой пробой. Реальная работа mac-1/phone после отзыва и зашифрованная PKI-копия остаются pending.


## Проверка устройств и внешняя PKI-копия — подтверждение пользователя

После отзыва acceptance-device пользователь подтвердил успешный вход в приложение с Mac и телефона. Затем выполнен сценарий: создание тестовой задачи на Mac → обновление страницы телефона → завершение задачи на телефоне → обновление страницы Mac. Пользователь подтвердил, что всё работает. Это проверка чтения/записи с двух устройств с ручным обновлением страницы; она не доказывает push-синхронизацию, конфликт одновременных записей либо все сочетания Wi-Fi/mobile/VPN.

Команда pki-backup завершилась сообщением hdiutil `created`: зашифрованный архив `~/simple-budget-backups/pki-20261125.dmg` создан на Mac после экспорта registry с отозванным тестовым устройством. Успешное создание подтверждено выводом пользователя; открытие/проверка содержимого архива и независимая копия на другом носителе ещё не подтверждены. Пароль архива в чат и репозиторий не передавался.


## Browser download: независимая проверка файла на Mac

Пользователь подтвердил скачивание полного архива через браузер (после инструкции проверить Mac и телефон). На Desktop Mac найден `budget-20261125T121114Z-9e88c86a1d0fff62.tar.gz`, размер50576B. Локально пересобран только изолированный административный helper, выполнен `admin backup verify --archive`: PASS; installation ID совпадает с acceptance, schema14, ledger/checksums, SQLite integrity/foreign keys/icons, financial JSON и хеши содержимого проверены штатным verifier. SHA256 всего файла `5e121a4d77f32279f211865ebf931936deaee6a3639722526d4e148240e6e49e` точно совпал с ранее полученным серверным status. Восстановление не выполнялось, рабочий бинарник и пользовательские данные Mac не менялись. Файл на телефоне отдельно не проверялся по контрольной сумме.


## PKI DMG: открытие проверено пользователем

Пользователь открыл зашифрованный DMG паролем и подтвердил наличие ключей. Предоставленный device-registry.json из архива совпадает с актуальным состоянием: mac-1/phone allowed, acceptance-device revoked25ноября12:59:03UTC, отпечатки совпадают с результатом отзыва. После проверки пользователь подтвердил извлечение образа. Проверка открытия и наличия ключей/registry завершена; фактическое восстановление PKI в отдельном окружении и дополнительная копия на независимом носителе этим не подтверждаются.


## Продолжение технической приёмки: Linux тестовые ELF

25ноября после13:16UTC read-only SSH подтвердил active/running app/Caddy, NRestarts0, current6233199, действующие TLS/schedule timers, daily next26ноября00:30UTC, около8.3GiB свободно. На VPS под непривилегированным budgetadmin запущены собранные на Mac Linux amd64 тестовые ELF, GOMAXPROCS=1, GOMEMLIMIT=192MiB, timeout9m; Go/Node/Playwright на VPS не устанавливались. Данные только временные test-marked каталоги.

Основной набор:33 PASS. Backup/restore с сохранением содержимого, повреждение, старая совместимая схема, контроль SHA и вредоносных архивов, concurrent downloads, ротация/pins/daily replay на изолированных тестовых часах, реальный SIGKILL worker на трёх фазах restore с finish/rollback, parent lease, streaming memory, production guards, реальный локальный HTTP reverse proxy. Первый запуск:32PASS/1FAIL — тесту старой схемы недоставало SQL fixtures; добавлены только файлы миграций в временный каталог, полный выбранный набор повторён и прошёл. Это не дефект приложения.

Planner набор:41PASS/1SKIP. Beta3/3.1/3.2, серии, materializer, экспорт/восстановление, иконки, ledger. Единственный SKIP — существующий TestBeta31PrivateSnapshot, поскольку частная пользовательская база намеренно не передавалась. Логи: artifacts/server-acceptance/runtime/linux-main-tests-fixtures.log и linux-planner-tests.log (не Git). Это реальное Linux выполнение синтетических тестов; оно не заменяет проверку установленного systemd unit, всех браузерных сценариев и таймерных событий.

Подготовлен одноразовый `scripts/ops/acceptance_runtime.py` для sudo на VPS: фиксированные INSTANCE=acceptance и installation ID, обязательный synthetic marker, отсутствие pending journals, общий административный lock, существующий чистый release. Права и flock проверяются без записи. Единственное изменение установленной acceptance — штатный manual backup с существующим systemd controller. Нагрузка100/1000, порча SQLite и restore выполняются только на свежей копии в /var/tmp; проверяются канонический хеш всех строк/ID/BLOB/history, финансовые bytes и повторный запуск. Нехватка места проверяется на16MiB tmpfs в отдельном mount namespace, без заполнения системного диска. Live Caddy/registry не меняются; проверяется их SHA. Полное исполнение этого сценария ещё pending до пользовательского sudo. Локальные ops68/68 PASS, py_compile/diff-check PASS; новые5тестов проверяют fail-closed область, digest/foreign keys, отказ linked/corrupt snapshot, таймаут дочернего процесса и отсутствие stderr/секретов в ошибке.


## Установленный Linux runtime: права, нагрузка и повреждённая копия — PASS

Пользователь выполнил проверенный одноразовый sudo-сценарий25ноября13:34:23UTC, exit0. Проверены фактические права UID/GID budget-acceptance: writable live, запрет записи release/administrative parent/backups, запрет чтения credentials/staging/pins/Caddy PKI, чтение опубликованного архива без возможности изменить/удалить его. Реальные flock родительского каталога и planner.db.lock удерживаются сервисом, admin check при работающем live отказывает. ELF на отдельной production-marked копии отказывает без пароля, при неверном ID, отсутствующем каталоге, попытке test environment; пустое хранилище не создаёт.

Две реальные TLS policies проверены, точный allowlist mac-1/phone, tickets явно отключены. Caddy/registry SHA до и после неизменны. Штатный manual backup `20261125T133424Z-1dfc96c9c964f5c8`, SHA256 `916c19014712df5a4310098cbdf84b717c2a833a00a4be40ea97363d65d7998c`, занял2.03s; наблюдаемый интервал недоступности TCP0.758s (шаг50ms, это не точное время HTTP downtime). После снимка настоящий acceptance снова прошёл authenticated readiness.

| Проверка на отдельной временной копии | Результат |
|---|---|
| Idle RSS настоящего приложения / Caddy | 20700 / 47904KiB |
| Добавление100проектов/1000задач, включая подзадачи | 3.997s |
| API tasks / projects / December calendar (loopback) | 64.01 / 107.47 / 32.66ms |
| Создание20серий и материализация | 5.824s;1680выполнений |
| Peak RSS процесса под нагрузкой (VmHWM) | 29372KiB |
| WAL под нагрузкой | 5265392B |
| Backup нагруженной копии | 1.062s; sampled peak RSS26028KiB |
| Verify архива | 0.291s |
| Восстановление намеренно повреждённой копии | 0.769s; sampled peak RSS22700KiB |
| Повторный startup/readiness копии | 0.325s |

После restore совпал канонический hash содержимого всех таблиц: `81fd436db6464597a5cdda24832b3dcf8be37f6f7528967ec138fb224906c1fe`, включая2682задачи,101проект,4485history events,20серий,1680occurrences,322иконки (одна новая пользовательская),1период работы. Непустые синтетические финансовые JSON совпали побайтно. Прежнее повреждённое поколение сохранено restore-протоколом до очистки всего временного стенда. Integrity/FK и чтение сохранённых ID после restart прошли. Отказ при недостатке места проверен на16MiB tmpfs в private mount namespace, системный диск не заполнялся.

Ограничения: API latency измерена без внешней сети/браузерного rendering; RSS CLI — дискретные выборки, а не гарантированный абсолютный пик. Изолированный нагрузочный процесс запускался с тем же установленным ELF/UID, GOMAXPROCS1/GOMEMLIMIT192MiB, но вне systemd unit sandbox. Проверка прав установленного unit отдельно выполнена на настоящих путях. CPU/load/swap во время пика отдельно не записывались. Не объявляется измеренным то, чего нет в JSON.

## Усиление TLS-приёмки

В старом Mac RevocationProbe найден дефект диагностики: OSError/timeout старого сокета принимался за закрытие. Исправлено: timeout старого канала теперь отказывает; для нового соединения reset/EOF и локальная ошибка TLS trust также не принимаются за доказательство отзыва. Добавлены регрессионные тесты. Предыдущий тестовый сертификат остаётся revoked.

Новая команда `make acceptance-tls ENV=production INSTANCE=acceptance DEVICE=acceptance-tls-device` использует отдельное временное удостоверение, четыре долгоживущих соединения TLS1.2/1.3 × HTTP/1.1/2, проверку401 без app password, наблюдение tickets после ответа, явный отзыв и проверку закрытия всех четырёх каналов. После отзыва принимает только TLS alert о сертификате; reset/EOF/timeout/ошибки доверия локального клиента не доказывают отзыв. TLS1.2 проверяется также с сохранённым session state. В TLS1.3 при отсутствии выданного ticket полноценного resumable session нет — это явно отражается в результате, не называется успешным тестом реального resumption. Семантика session events сверена с [официальной документацией Node TLS](https://nodejs.org/api/tls.html#event-session).

Worker запускается только на Mac, без npm-зависимостей, с очищенным окружением (NODE_OPTIONS/SSLKEYLOGFILE не наследуются); passphrase передаётся по stdin, SSLSession остаётся в памяти, ключи не передаются на VPS. Команда запрещает production, реальные устройства и повторное разрешение revoked leaf. При недоказанном результате после отзыва штатный access-stop закрывает публичный доступ. Сам протокол на VPS этой новой командой пока pending. Локально Python ops73/73 PASS; новый настоящий локальный TLS/HTTP2 тест2/2 PASS (временные CA/сервер, без настоящей PKI), включая отказ засчитывать остановленный сервер за успешный отзыв.


## Реальная TLS-матрица на VPS — PASS (25ноября13:53UTC)

Пользователь выполнил acceptance-tls, локальный evidence JSON независимо прочитан: `~/.simple-budget-admin/acceptance-tls-result.json`. Новый временный leaf acceptance-tls-device SHA256 `30ec9e6fc332dc43984d42720f775e7a9efabf51c44b7ef46d01490ea299e093` добавлен13:53:08UTC и отозван13:53:23UTC. mac-1/phone остались allowed, ранее отозванный acceptance-device не разрешался повторно.

Все четыре сочетания TLS1.2/1.3 × HTTP/1.1/h2 установили соединение с действующим тестовым cert и получили HTTP401 без app password. После чтения ответа tickets не наблюдались. Перед отзывом все четыре соединения были открыты, после штатного restart все четыре получили событие close. Шесть отрицательных подключений после отзыва получили `ERR_SSL_SSLV3_ALERT_BAD_CERTIFICATE`: новые h1/h2 для обеих TLS-версий плюс h1/h2 TLS1.2 с сохранённым session state. Это конкретный TLS alert, не TCP timeout/reset. Для TLS1.3 resumable session не появился, поскольку ticket не выдавался; успешный replay настоящего TLS1.3 ticket не заявляется.

Read-only SSH13:54UTC: ssh/Caddy/app active, NRestarts0, app/Caddy/daily/TLS enabled, backend только127.0.0.1:3002;3001/2019 отсутствуют. Current6233199. Baseline boot ID `455c3410-5a32-417e-9acc-49a00743520f` сохранён в ignored artifacts/server-acceptance/pre-reboot.json. Reboot ещё не выполнялся и требует отдельного согласования пользователя. После второго тестового удостоверения локальный device-registry.json обновлён; прежний PKI DMG содержит более ранний registry, нужна новая зашифрованная копия с новым именем.


## Согласованный reboot VPS — сервисы и защита запустились

Пользователь выполнил sudo reboot25ноября14:13:09UTC. Независимый SSH с прежним strict host key успешно подключился14:13:31UTC. Boot ID изменился с455c3410-5a32-417e-9acc-49a00743520f на82ad9a56-fef4-47ac-97e9-7575755cab1d. SSH, Caddy и acceptance active/running; NRestarts0, failed units0. UFW unit active/exited, Result=success, ExecMainStatus0 (это подтверждение успешного запуска unit; полный вывод правил после reboot отдельно не получен).

App/Caddy/daily/TLS включены в автозапуск. Backend только127.0.0.1:3002, listeners3001/2019 отсутствуют; публичные22/80/443 сохранились. Current6233199. Daily next26ноября00:30UTC=03:30МСК; значение LAST timer само по себе не доказывает успешный daily, его receipt ещё ожидается. Доступно570MiB RAM, swap использовано0MiB, выборка непосредственно после запуска.

Внешний HTTPS без клиентского cert отклонён: TLS1.2 HANDSHAKE_FAILURE, TLS1.3 CERTIFICATE_REQUIRED. В TLS1.3 проверена доверенная цепочка/SAN171.22.237.16, serial прежний06C2AC4FA4A90AB446B1395D4B0F149D85FA, expiry1декабря12:46:10UTC; renewal не форсировался и пока не подтверждён. Прямые localhost запросы без пароля к `/`, `/api/ready`, `/api/planner/tasks`, `/api/backups/status`, `/js/app.js` вернули401.

Результаты в ignored post-reboot.json и post-reboot-tls.json. Положительный браузерный доступ Mac/phone и наличие тестовых задач после reboot ещё ожидают подтверждения пользователя. Полный root status и сверка содержимого live после reboot отдельно не выполнялись.


## После reboot: данные сохранены, конфликт расширения Chrome устранён

Пользователь подтвердил сохранение задач после reboot, работу телефона и Mac incognito. Обычный профиль Chrome зависал даже на `/api/ready`; backend независимо возвращал401 с корректным WWW-Authenticate, app/Caddy active/NRestarts0. После временного отключения расширений запрос прошёл, поочерёдное включение выявило VPN-расширение. Пользователь удалил его, обычный профиль получил200 и readiness JSON revision6233199/schema14/modified=false. Это подтверждённый клиентский конфликт расширения, не сбой серверного startup; серверная защита не ослаблялась.

Подготовлена `make acceptance-http ENV=production INSTANCE=acceptance DEVICE=mac-1`: проверяет реальный protected HTTPS после reboot, авторизацию API/static/backup, Host/Origin/Sec-Fetch-Site/Content-Type, отказ forwarded bypass, недоступность raw data/staging/HTTP restore. Скачивает один готовый архив через два Range206-запроса с проверкой Content-Range/ETag/no-store/attachment и SHA256, затем выполняет native archive verify. Отдельно скачивает planner SQLite, сверяет manifest hash, integrity/FK. Проверяет исходный task/project ID, если имеется сохранённый fixture receipt (для текущей приёмки он есть в ignored artifacts). Все неподходящие POST содержат только пустой объект, не команды создания или восстановления. Приложение не останавливается; данные не восстанавливаются; временные скачанные файлы удаляются. Пока фактический HTTP-прогон pending. Локальные Python ops76/76 PASS, py_compile/diff-check PASS; новые тесты покрывают правильную сборку Range, статусы/политику/ETag/границы/обрезание/лишние bytes/неверный SHA и запрет другой области/избыточного размера.


## Финальная HTTP-проверка через Caddy после reboot — PASS

25 ноября 2026, 15:12:55 UTC: пользователь выполнил acceptance-http; локальный файл `~/.simple-budget-admin/acceptance-http-result.json` независимо прочитан. Все 28 проверок прошли на настоящем HTTPS с mac-1, installation ID6919a32cbb5e4cd09a2a00f691e32812, revision623319941eac5fadc48ef1143c3af7b8743ebbdc/schema14. Без пароля HTML/JS/CSS/readiness/planner/backup status/full/planner download возвращают401; с авторизацией встроенные ресурсы200. Forwarded headers не обходят авторизацию, Host или Origin; чужой Origin403, чужой Host404, неверный Content-Type415, cross-site403, неверный пароль401. Raw SQLite/JSON, staging, traversal и HTTP restore недоступны.

Исходный task ID и его project ID сохранены после restore/update/rollback/reboot (одна известная исходная задача; это не полная сверка всех строк live). Архив20261125T133424Z-1dfc96c9c964f5c8 скачан двумя Range206, размер51687B, SHA256916c19014712df5a4310098cbdf84b717c2a833a00a4be40ea97363d65d7998c. Проверены Content-Range, ETag, attachment, no-store, native archive verify. Отдельный planner download581632B соответствует manifest SHA25615993411972f5d673dbabcf1abf403c1698958999159392ad746deaf9767d433; SQLite integrity/FK PASS. Сервис не останавливался, restore и изменения пользовательских данных не запрашивались.

Root status перед прогоном после reboot: app running, Caddy active, proxy open/configuration_matches=true; deployment/restore/backup/revocation pending отсутствуют. Оба настоящих устройства allowed, оба временных revoked. MemAvailable576308KiB, swap0, load0.00, свободно8860487680B. Daily enabled, next26ноября00:30UTC, daily_count0. TLS serial прежний, остаток141.56h; первое автоматическое продление ещё не произошло.

Ближайшие обязательные действия: обновить отдельный зашифрованный PKI DMG с обоими revoked устройствами; подтвердить успешный автоматический daily после26ноября03:30МСК; подтвердить фактическое автоматическое renewal в ожидаемое окно. Эти ожидаемые события не заменяются настройкой timers. Ограничения измерений и непроверенные части полного чек-листа выше сохраняются; окончательная production-приёмка и перенос настоящих данных не объявляются завершёнными.


## Обновлённая зашифрованная PKI-копия — проверена пользователем

25 ноября пользователь создал `/Users/mac/simple-budget-backups/pki-20261125-final.dmg` штатной командой pki-backup. После инструкции открыть образ паролем предоставил содержимое device-registry.json: mac-1 и phone allowed, acceptance-device и acceptance-tls-device revoked; отпечатки и времена отзыва совпадают с результатами серверных проверок. Актуальность registry в новой копии подтверждена пользователем. Извлечение образа, отдельный независимый носитель и полное аварийное восстановление PKI этим сообщением не подтверждены. Приватные ключи и пароли в отчёт не включались.

Следующая проверка: успешный автоматический daily 26 ноября в03:30МСК (receipt/verify, а не только статус timer); затем фактическое автоматическое продление HTTPS. До этих событий окончательная production-приёмка остаётся открытой.

# Server Deployment 3 — эксплуатация с Mac

Статус: подготовлено локально 25.11.2026; команды ниже **ещё не выполнялись на VPS**. Linux/systemd/Caddy-приёмка обязательна до настоящих данных. [Отчёт](SERVER-DEPLOYMENT-STAGE-3.md), [чек-лист](SERVER-ACCEPTANCE.md), [формат backup и recovery](BACKUP-RECOVERY.md).

## Предварительные условия

Административный Mac: Git, Make, Python 3.9+, Go toolchain из go.mod, штатные SSH/SCP, OpenSSL, hdiutil. GNU coreutils на Mac не нужны. macOS 12.7 поддерживается кодом скриптов, но выполнение PKCS#12/Keychain на втором Mac остаётся проверкой устройства. Скрипты не устанавливают зависимости автоматически.

SSH alias `budget` должен разрешаться в `budgetadmin@171.22.237.16`, host key уже проверен пользователем. Каждая команда принудительно использует `StrictHostKeyChecking=yes`; ошибку ключа нельзя обходить удалением known_hosts без независимой проверки. sudo остаётся интерактивным: нет NOPASSWD и нет произвольного root shell в установленном wrapper.

**Перед первым release нужно проверить и зафиксировать изменения в Git.** `make release-linux`, installer и deploy отказывают при dirty tree. Этот этап не делает commit/push. Диагностическая кросс-сборка с `modified=true` непригодна для установки. Не коммитить данные, ключи, архивы и artifacts.

Сервер и рабочее локальное приложение — разные установки. `ENV=production` означает выбор настоящего сервера. `INSTANCE=acceptance` означает отдельную синтетическую установку на этом сервере. Для изменяющих команд пишите оба параметра явно. По умолчанию INSTANCE=acceptance, обычный `make` показывает только help.

## Первоначальное тестовое развёртывание

После отдельного разрешения подключаться к VPS и после чистого проверенного commit, на Mac из корня репозитория:

```sh
make help
make test-ops
make release-linux
make server-install ENV=production
make initialize ENV=production INSTANCE=acceptance DEVICE=mac-1
make acceptance ENV=production INSTANCE=acceptance DEVICE=mac-1
make status ENV=production INSTANCE=acceptance
make tls-status ENV=production INSTANCE=acceptance
make backup ENV=production INSTANCE=acceptance
make fetch-backup ENV=production INSTANCE=acceptance
```

`server-install` проверяет Ubuntu 24.04, amd64, RAM, диск/inode, существующий Caddy **2.11.9** и его реальный адаптированный JSON. При другой версии или несовпадающем Caddyfile требуется разбор, не автоматическая замена. Сохраняет исходный Caddyfile, создаёт пользователей/каталоги, устанавливает wrapper/units; **не создаёт app database и не меняет действующую TLS-страницу**. Включаются только диагностические TLS/schedule timers.

`initialize` просит подтверждение, новый пароль приложения минимум 12 символов дважды (рекомендуется 16), sudo, пароль зашифрованного ключа mac-1 и пароль приложения для HTTPS-проверки. Запишите пароль в менеджер паролей. Пароль вводится скрыто, не через аргументы/env. CA и клиентские ключи на VPS не передаются. Минимум одинаков для backend, password file/systemd credential и административного ввода. Считаются символы Unicode, максимум — 4096 байт UTF-8 без завершающего перевода строки. Уже заданные длинные пароли остаются действительными. mTLS, Basic Auth и ограничение неверных попыток сохраняются.

Если административные скрипты уже установлены с прежним минимумом 32, **до initialize** повторите `make server-install ENV=production` из актуального чистого checkout, затем проверьте успешный результат. Эта команда обновляет скрипты, не инициализирует данные и не переключает Caddy. Одной пересборки Go недостаточно.

После initialization публичный IP временно ведёт к синтетическому приложению. Синтетическая установка: `budget-acceptance`, loopback3002, `/var/lib/simple-budget-acceptance/live`, собственный ID, release/config/backup. Production: `budget`, loopback3001 и `/var/lib/simple-budget/live`. Данные и OS-права разделены. Две установки не следует постоянно держать активными на 864 MiB RAM; перед настоящим импортом синтетическую нужно остановить и отключить её backup timer (команды ниже).

`acceptance` работает **только** с синтетическим ID и проверенным публичным маршрутом на acceptance; создаёт специально названные тестовые проект/задачу, проверяет GET/POST, Host/Origin, пароль, отсутствие клиентского сертификата, backup stop/start и потоковое HTTP-скачивание с повторной CLI-проверкой архива. Не запускать браузерные тесты против production. Полный серверный чек-лист этим smoke не заменяется.

Для возврата защищённой тестовой страницы:

```sh
make secure-page ENV=production INSTANCE=acceptance
```

Она использует **актуальный** allowlist устройств; исходный Caddyfile после отзывов нельзя просто вернуть копированием. Caddy admin API выключается при переходе на управляемую конфигурацию; применяются `validate` и полный restart. Это закрывает существующие HTTPS-соединения, поэтому изменение конфигурации кратковременно прерывает скачивания.

## Каталоги, права и systemd

| Назначение | Production |
|---|---|
| Код, только root пишет | `/opt/simple-budget/releases/<revision>-<sha>/budget`, `release.json`; `current`, `previous` |
| Настройки/пароль | `/etc/simple-budget/configuration`, `identity.json`, `credentials/app-password` |
| Live, budget:budget 0700/0600 | `/var/lib/simple-budget/live` |
| Административный родитель root:budget 0750 | `/var/lib/simple-budget`: deployment journal, прежние поколения, кандидаты |
| Backup root:budget 0750 | `/var/backups/simple-budget`: daily/pre-update/manual/pins/staging |
| Общий admin state root 0700 | `/var/lib/simple-budget-admin`: операции, исходный Caddy, публичный PKI registry, TLS observations |
| Установленный фиксированный CLI | `/usr/local/sbin/budget-admin`, `budget-controller`; код `/usr/local/lib/simple-budget` |
| Управляемый Caddy | `/etc/caddy/simple-budget.json`; исходный Caddyfile сохранён и не переписывается |

В acceptance добавляется суффикс `-acceptance`, включая пользователя. Backup-файлы публикуются root:budget 0640, директории 0750; web может читать готовые копии, но не создавать/удалять их. Staging/journals/credentials недоступны web. Parent-directory flock открывается на чтение; writable parent для приложения не требуется. Symlink для данных запрещён, для release разрешён только проверяемый pointer внутри releases.

App unit: `LoadCredential`, loopback, check-only migrations, Moscow TZ, graceful SIGTERM/60s, restart-on-failure с лимитом, `NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp/Devices`, writable только live, закрыты каталоги Caddy. `MemoryHigh=320M`, `MemoryMax=448M`, `MemorySwapMax=256M`, Go memory target256MiB/GOMAXPROCS1. Это стартовые ограничения, не результат VPS-нагрузки. Journald ограничен128MiB/14дней (применение глобального drop-in — после штатного restart journald/перезагрузки при приёмке).

## Backup, скачивание и обслуживание

```sh
make backup ENV=production INSTANCE=production
make fetch-backup ENV=production INSTANCE=production
make fetch-backup ENV=production INSTANCE=production BACKUP_ID=20261125T000000Z-0123456789abcdef
make status ENV=production INSTANCE=production
make logs ENV=production INSTANCE=production
```

Последняя команда показывает безопасную сводку ошибок, а не raw HTTP bodies/пароли. Подробный journal доступен администратору через обычный `ssh budget` и `sudo journalctl -u simple-budget --since today --no-pager`; не публикуйте его без проверки персональных данных.

Daily timer `simple-budget-backup@production.timer`: 03:30 Europe/Moscow, Persistent=true. UTC сервера менять не нужно. CLI не создаёт вторую daily за ту же московскую дату. Хранятся7 успешных daily,2 обычных pre-update, pins не ротируются. До компрессии обычного daily/manual сервис возобновляется. Deployment удерживает остановку. Предупреждение устаревания единообразно **26 часов**. Расписание в UI — наблюдение реального systemd, обновляется после backup/commit и ежечасно; отсутствие timer не отображается как «включено».

Диск: минимум max(2GiB,15%) плюс staging/распаковка/кандидат; проверяются inode. При недостатке места операция отказывает, историю не удаляет. Защищённые/manual/previous-data не очищаются по возрасту без решения администратора.

```sh
make cleanup ENV=production INSTANCE=production KIND=backup BACKUP_ID=<ID>
```

Показывается подтверждение. Копии pending/последнего deployment/rollback pair и последняя исправная копия защищены. Для старой неиспользуемой pin выполняется explicit unprotect+проверяемое удаление. После восстановления прежнее live-поколение остаётся root-only; его удаление требует остановленного приложения, правильного ID, проверенной актуальной копии и отсутствия журналов:

```sh
ssh -t budget sudo systemctl stop simple-budget.service
make cleanup ENV=production INSTANCE=production KIND=previous-data NAME=.live.previous-<ID>
ssh -t budget sudo systemctl start simple-budget.service
make status ENV=production INSTANCE=production
```

Не заменять эти команды `rm -rf`. Неизвестные/частично инициализированные каталоги сохраняются для отдельного разбора. Автоматической «очистки всего» нет.

## Обновление и прерывания

```sh
make deploy ENV=production INSTANCE=production DEVICE=mac-1
```

Сначала clean Git/Linux build/manifest/SHA/version/schema, upload только binary+manifest. Затем общий неблокирующий operations lock; Caddy503; остановка и protected pre-update; restore+forward migration на кандидате; явная live migration; release pointers; запуск и authenticated local readiness; наружу разрешён только `/api/ready`. Mac проверяет доверенный HTTPS, mTLS, пароль, commit/schema. При HTTP401 команда предлагает до трёх попыток ввода только пароля приложения, сохраняя разблокированный TLS context. Серверная операция и sudo из-за опечатки не повторяются. После трёх отказов или отмены требуется штатный recover; pending сохраняется. При HTTP429, других ошибках HTTP или сети автоматического повтора нет; серверное ограничение частоты авторизации сохраняется. Только после этого durable boundary `opening`, открытие пользовательских запросов и commit. Current/previous остаются; остальные проверенные release удаляются. CLI предыдущей версии никогда не запускается поверх новой несовместимой схемы.

При разрыве SSH не повторяйте deploy вслепую. На Mac сохранён request ID, на сервере durable journal. При незавершённой операции новая запрещена:

```sh
make status ENV=production INSTANCE=production
make recover ENV=production INSTANCE=production DECISION=finish DEVICE=mac-1
# Либо ДО открытия пользовательских записей:
make recover ENV=production INSTANCE=production DECISION=rollback DEVICE=mac-1
```

`finish` применим к trial/committed/initialization/сохранённому restore; незавершённая миграция требует rollback к проверенному предыдущему набору. При `opening`/`committed` возможны новые записи: автоматический data rollback запрещён. Потерянный ответ успешного commit сверяется по ID, не создаёт новую операцию. Прерванный обычный backup восстанавливается `recover ... DECISION=finish` через stage2 journal/controller. При отсутствии локального pending после потери Mac проверьте серверный журнал вручную, не удаляйте его ради повторного запуска.

Явный откат уже завершённого обновления **восстанавливает данные pre-update**, то есть может потерять новые изменения. Отдельное подтверждение обязательно; сначала сохраняется свежая protected копия текущих данных:

```sh
make rollback ENV=production INSTANCE=production DEVICE=mac-1
```

Если Caddy validation/restart/отзыв не удался, доступ остаётся закрытым; нет fallback без mTLS. Не удаляйте journal и не запускайте старый бинарник вручную. Обрыв ещё до создания серверного журнала разрешается `recover DECISION=finish`: команда сверяет отсутствие незавершённого изменения и освобождает локальный pending token, не меняя данные.

## Перенос настоящих локальных данных

Делать только после синтетической приёмки и отдельного разрешения остановить Mac-приложение. Скрипт **не ищет и не убивает процессы**. Определите фактический каталог по параметрам запуска/конфигурации; не угадывайте по текущей директории. Оригинал не открывается тестовым Store.

1. Сохранить независимую локальную копию. Штатно остановить единственный локальный процесс; получить отдельное согласие пользователя на остановку. До завершения переноса не создавать новые записи на Mac.
2. Для legacy-каталога без нового production marker:

```sh
make legacy-export LOCAL_DATA=/absolute/path/to/actual/local/data
```

Введите `ОСТАНОВЛЕНО` только после фактической остановки. CLI проверяет parent lease и существующий SQLite flock, копирует DB+WAL и JSON в приватный промежуточный каталог, валидирует/экспортирует SQLite **только на копии**, проверяет неизменность исходных bytes. Исходный marker не создаётся/не меняется. Новый transfer ID относится к клону; snapshot/data.json/categories.json сохраняют данные и ID сущностей. Результат — обычный полный проверенный stage2 archive в `~/simple-budget-backups/migration/transfer-<ID>`. Он остаётся на Mac.

Для уже маркированной production-установки использовать обычный полный stage2 backup с существующим ID, а не legacy-export. Экспорт в веб-интерфейсе SQLite отдельно от JSON не заменяет полный перенос.

3. Остановить synthetic app/timer, не удаляя данные:

```sh
ssh -t budget sudo systemctl disable --now simple-budget-acceptance.service simple-budget-backup@acceptance.timer
make migrate-data ENV=production INSTANCE=production ARCHIVE=/absolute/path/to/verified/backup.tar.gz DEVICE=mac-1
```

Серверная production installation создаётся **с ID архива**, поэтому stage2 identity check не обходится. Существующая installation не перезаписывается. Промежуточный transfer ID и финальный server ID совпадают. Проверяются manifest/SHA/schema/ledger/иконки/JSON, владельцы и authenticated HTTPS.

4. Сверить количество/ID задач, проектов, серий, истории, иконок, финансовых записей/категорий; проверить календарь и независимые даты. CLI гарантирует целостность переносимого архива, но содержательную пользовательскую сверку выполнить отдельно. Скачать свежий полный backup на Mac и на независимый носитель.
5. VPS становится единственным источником данных. Сохранённую локальную установку не запускать для новых записей; разработка использует test-marked изолированные каталоги. Перенос не удаляет оригинальные данные и не перезаписывает рабочий `./budget`.

## Восстановление и потеря VPS

```sh
make restore ENV=production INSTANCE=production ARCHIVE=/absolute/path/verified.tar.gz DEVICE=mac-1
# Только при реально повреждённом live и после отдельного подтверждения:
make restore ENV=production INSTANCE=production ARCHIVE=/absolute/path/verified.tar.gz ALLOW_CORRUPT_LIVE=yes DEVICE=mac-1
```

Обычное restore создаёт свежую pre-update копию до изменения данных. При повреждении SQLite не требуется запускать Store; флаг разрешает отсутствие свежей копии, старый каталог всё равно сохраняется. Installation ID обязан совпадать. Полный restore — стандартный безопасный вариант; planner-only остаётся административным CLI stage2, не HTTP и не отдельной кнопкой.

После полной потери сервера:

1. Восстановить SSH/UFW/поддерживаемый Caddy отдельно, до открытия приложения; без клиентского сертификата соединение обязано отвергаться. На новом сервере публичный IP может отличаться: текущая автоматизация закреплена за171.22.237.16, другой адрес требует осознанного изменения конфигурации/проверок.
2. Восстановить публичную CA и **последний актуальный** allowlist/отзывы из отдельной PKI-копии. Не откатывать отзывы вместе с пользовательским backup. Закрытый CA/key/p12 на VPS не копировать.
3. Получить новый серверный LE IP certificate штатным Caddy ACME, сохраняя mTLS во всех TLS policies. Это отдельная операция disaster recovery, не функция пользовательского архива. Не восстанавливать чужой/устаревший ACME private state из app backup.
4. После проверки Caddy выполнить `server-install`, затем `migrate-data ... ARCHIVE=...` на пустой production installation с ID сохранённого архива. Секрет приложения создать заново через скрытый prompt.
5. Пройти HTTPS/device/backup/reboot приёмку. Сверить данные, скачать новую внешнюю копию. Первый фактический automatic renewal проверить отдельно.

## Устройства, PKI и отзыв

```sh
make client-list ENV=production
make client-issue ENV=production DEVICE=mac-2
# Импортировать ~/.simple-budget-pki/mac-2.p12 на второй Mac (login Keychain).
make client-add ENV=production DEVICE=mac-2
```

CA не создаётся заново. Подписание происходит только на Mac зашифрованным существующим CA key. Пароли запрашивает OpenSSL, они не передаются `-pass...`, env или shell args. На VPS отправляется единственный публичный `.crt`; CA key/client key/p12 туда не отправляются. Импорт Android — удостоверение для VPN/приложений, затем проверить Chrome и future installed PWA отдельно. Передача p12 — защищённым каналом, пароль отдельно; не Git/email/logs.

Продление создаёт новое имя и отпечаток, сохраняя старое до проверки:

```sh
make client-renew ENV=production DEVICE=mac-1 NAME=mac-1-2027
make client-add ENV=production DEVICE=mac-1-2027
# Импорт/успешная проверка нового удостоверения, затем:
make client-revoke ENV=production DEVICE=mac-1
```

Отзыв показывает устройство+SHA-256, требует `ПОДТВЕРЖДАЮ`. Mac открывает тестовое старое соединение, root останавливает Caddy, durable registry помечает сертификат revoked, все TLS policies строятся из нового allowlist, полный restart закрывает HTTP/2 и keepalive. Mac проверяет закрытие старого канала и отказы нового/повторного TLS-сеанса. До остановки записывается durable intent отзыва. Перед каждым запуском Caddy root guard сверяет registry и все TLS policies: прерывание между записью intent и новым конфигом блокирует запуск даже после reboot. Незавершённый отзыв того же устройства можно продолжить повторной командой client-revoke; другие PKI-изменения до его завершения запрещены. Если probe не доказывает отзыв — Caddy оставляется остановленным. Последнее разрешённое устройство отозвать этой командой нельзя. SSH остаётся независимым доступом.

Для потерянного устройства probe использует зашифрованную копию его ключа, оставшуюся на административном Mac. Если её тоже нет, текущая удобная команда откажет до изменения; администратор должен отдельно закрыть Caddy и провести контролируемое исключение публичного leaf с проверкой по временному эквивалентному сценарию. Не считать сетевой timeout доказательством отзыва.

На разработке действующие удостоверения не трогать. При VPS-приёмке выпустить `acceptance-device`, добавить, проверить, отозвать, затем проверить действующие mac-1/phone. Ни новый CA, ни production server certificate для этого не нужны.

Caddy2.11.9 в исходниках отключает session tickets при client auth; `VerifyConnection` проверяет верификаторы и при resumption. Управляемый JSON дополнительно задаёт `session_tickets.disabled=true`. Это исходный контракт, не факт измерения VPS: [connpolicy.go](https://github.com/caddyserver/caddy/blob/v2.11.9/modules/caddytls/connpolicy.go), [sessiontickets.go](https://github.com/caddyserver/caddy/blob/v2.11.9/modules/caddytls/sessiontickets.go). Проверить отсутствие ticket/resumption на реальном TLS1.2/1.3 после restart; старые подключения закрываются полным restart.

Отдельная административная копия PKI:

```sh
make pki-export ENV=production
make pki-backup ENV=production
```

Скрипт требует свежий (<24h) registry, создаёт AES-256 encrypted DMG средствами macOS вне репозитория; пароль интерактивный. Внутри encrypted CA/device keys, p12 и актуальный публичный registry. Хранить на независимом защищённом носителе. Пользовательский backup приложения **этого не содержит**. После каждого отзыва обновить внешнюю PKI-копию; старые копии не являются актуальным списком разрешений.

## TLS и диагностика

```sh
make tls-status ENV=production
make client-list ENV=production
make status ENV=production INSTANCE=production
```

Hourly timer проверяет реально обслуживаемый leaf через loopback443 без хранения client private key на сервере; отдельно валидирует публичную цепочку/IP SAN. Показывает serial/issuer/SAN/notBefore/notAfter/остаток, Caddy state, ограниченную сводку ACME errors и успешных certificate events. <48h warning, <24h critical; nonzero exit. Смена наблюдаемого serial и событие automatic renewal различаются. Наличие timer не доказывает продление. Первое окно, сообщённое пользователем:28ноября06:40–09:50МСК; проверить29ноября без forced renewal, удаления cert или изменения часов.

Status включает OS/uptime/RAM/swap/load/disk/inodes, release/schema, DB/WAL size, backup metadata/counts/pins/schedule, deployment/restore pending, клиентские сроки, TLS и reboot-required. Ошибки materializer не скрываются; отменённые клиентом запросы не записываются как internal500.


## Исправление незавершённой synthetic initialization (systemd credential ACL)

Ubuntu systemd 255 может создавать credential `root:root 0440` с ACL: root r--, именованный UID сервиса r--, группа --- и other ---. Group mode bits здесь отражают маску ACL, а не доступ группы. Linux reader проверяет точный ACL и UID процесса через открытый файловый дескриптор; обычный `BUDGET_PASSWORD_FILE` по-прежнему требует owner-only, без symlink/hardlink. При любой неоднозначности — отказ. На других ОС сохраняется прежняя строгая проверка. Минимум пароля — 12 символов, рекомендуется 16.

Если первая acceptance-инициализация остановилась до открытия публичного доступа из-за старого credential reader, повторный installer/deploy запрещён незавершённым журналом. Для этого узкого случая:

```sh
make repair-initialization ENV=production INSTANCE=acceptance DEVICE=mac-1
# После проверки результата initialization_release_repaired:
make recover ENV=production INSTANCE=acceptance DECISION=finish DEVICE=mac-1
```

Первая команда требует подтверждение, чистый commit, тот же pending installation/operation ID, synthetic marker, остановленный сервис, initialize/trial journal, прежнюю схему и ещё не переключённый proxy. Загружает чистый release и ограниченный repair helper, использующий уже установленные административные функции и единый lock. Проверяет SQLite через `admin check`, затем запускает новый ELF через настоящий transient systemd unit с LoadCredential и **синтетическим** паролем. Read-only `budget admin check-credential` проверяет только credential и выводит `{"credential":"valid"}`, никогда значение пароля. Production data/config для пробы не загружаются.

После успешной пробы создаётся защищённый pre-update backup, записывается recovery journal и переключается только release pointer. Данные/схема/пароль не пересоздаются и не мигрируются, Caddy не меняется. Прерывание между журналом и pointer оставляет startup закрытым; команду можно повторить после проверки состояния. Оригинальный pending ID на Mac сохраняется. Вторая команда использует штатные start/readiness, mTLS HTTPS proof и commit; только после этого снимается pending. Рабочие либо уже опубликованные установки этим helper ремонтировать нельзя. Защищённая repair-копия сохраняется отдельно до проверки зависимостей и явной административной очистки.

Это не универсальный обход recovery: для production, другого вида операции, схемы или открытого proxy команда отказывает. Первая команда не требует повторной установки инфраструктуры и не меняет SSH/UFW/сертификаты. Sudo вводится в терминале; при `recover` потребуются пароль ключа устройства и существующий пароль приложения.


## Повтор только HTTPS-скачивания acceptance backup

Если synthetic smoke уже создал задачи и проверенный архив, повтор скачивания не требует повторения этих действий:

```sh
make acceptance-download ENV=production INSTANCE=acceptance DEVICE=mac-1 BACKUP_ID=<проверенный-ID>
```

Потребуются только пароль зашифрованного ключа устройства и пароль приложения. Sudo, создание нового backup и остановка сервиса не выполняются. Команда проверяет mTLS/HTTP200, no-store (включая одинаковые заголовки proxy/upstream), attachment, потоково скачивает архив и проверяет его через локальный административный helper, включая installation/backup ID. Готовая копия: `~/.simple-budget-admin/acceptance-<ID>.tar.gz`; receipt `download-<ID>.json`. Незавершённый файл не публикуется как проверенный. Эта команда не заменяет остальные этапы VPS-приёмки.


### Дополнительная синтетическая VPS-приёмка

`acceptance_runtime.py` — одноразовый проверяемый сценарий, не production endpoint и не установленный root-wrapper. После проверки файла администратор запускает его через `sudo /usr/bin/python3 -I /absolute/reviewed/acceptance_runtime.py`. Он привязан к текущей синтетической acceptance-установке; другой ID, production или pending recovery запрещены. Использует установленный ELF и административные модули, не заменяет releases/configuration. Создаёт manual backup штатным controller; во время снимка возможна короткая недоступность. Остальные изменения ограничены новой временной копией /var/tmp, включая нагрузку и преднамеренную порчу SQLite. Итоговая безопасная JSON-сводка сохраняется в /var/lib/simple-budget-admin/acceptance-results и выводится в терминал. При ошибке не считать частично выполненный сценарий успешным; сохранить сообщение и проверить status перед повтором. Прямой запуск с sudo нужен для реальных проверок UID, системных блокировок и private mount namespace, а не для обхода app auth.


### Отдельная TLS-матрица приёмки

```sh
make acceptance-tls ENV=production INSTANCE=acceptance DEVICE=acceptance-tls-device
```

Только синтетическая установка. На Mac необходим существующий Node (новые npm packages не нужны). Если отдельного тестового удостоверения нет, команда выпускает его существующим CA через обычные интерактивные OpenSSL prompts. Затем добавляет только публичный leaf, открывает четыре соединения TLS1.2/1.3 с HTTP/1.1/2, показывает fingerprint и требует подтверждение отзыва. Нужен пароль тестового ключа, но не пароль приложения: ожидаемый HTTP401 проверяет второй слой защиты. При успешном отзыве сохраняет `~/.simple-budget-admin/acceptance-tls-result.json` и актуальный device-registry.json. Повторно разрешать уже отозванное удостоверение эта команда не будет.

Действующие mac-1/phone не изменяются. Caddy штатно перезапускается при изменении allowlist. Если после отзыва TLS-проверка не доказала отказ, доступ закрывается через access-stop; не обходить ошибку ослаблением mTLS. Незавершённая подготовка может оставить тестовое устройство allowed: проверьте client-list и завершите отзыв. Incoming receipts остаются для диагностики и штатной последующей административной очистки. После успешного изменения registry создайте новую зашифрованную PKI-копию с новым именем, не перезаписывая прежнюю.


### Финальные HTTP-проверки синтетической установки

```sh
make acceptance-http ENV=production INSTANCE=acceptance DEVICE=mac-1
```

Нужны sudo для получения безопасного status, пароль ключа устройства и пароль приложения. При HTTP401 предусмотрены три попытки app password. Команда проверяет текущий synthetic ID/публичный маршрут и clean revision/schema, затем положительные GET и отрицательные проверки auth/Host/Origin/forwarded/Content-Type. Готовый полный backup читается двумя Range-запросами; planner download сверяется с manifest. Размер скачивания ограничен64MiB для синтетической проверки; обычные административные backup/restore этим лимитом не меняются. Итог сохраняется в `~/.simple-budget-admin/acceptance-http-result.json`. Новая резервная копия, restore, restart и изменение данных не запрашиваются. Неподходящие POST имеют пустой JSON, чтобы даже дефект проверяемого фильтра не создал валидную задачу.

## Server UX 1.1: меню, локальные копии и авторизация

Подробный локальный отчёт и ограничения: [SERVER-UX-1.1.md](SERVER-UX-1.1.md).

```sh
make server                            # меню, всегда начинает с acceptance
make server AUTH=manual SUDO_MODE=legacy # совместимо с уже установленным VPS
make keychain-build
make keychain-status INSTANCE=acceptance
```

На проверенном Mac12.7.6 Touch ID доступен, но DP Keychain ad-hoc helper вернул -34018 (недостающий entitlement). Поэтому сейчас используйте ручной режим. `AUTH=keychain` не обходит ограничение и не делает скрытый fallback. Регистрация/замена/проверка/удаление доступны через keychain-register/check/delete с SECRET=app|client|pki; успешная биометрическая регистрация требует отдельно проверенного provisioned helper. Секреты разделены по серверу, instance, installation ID и устройству. CA/sudo password не сохраняются. Реальное число biometric prompts не подтверждено.

Новый ограниченный `SUDO_MODE=session` работает только после согласованной установки административного обновления. Он вызывает sudo один раз, использует отдельный PTY и фиксированный план существующих handler; между командами меню привилегии не сохраняются. Idle120s/новые команды30мин, не более32 шагов; без изменения sudoers/timestamp. При ошибке status/recover, без автоматического повторения mutation. До обновления VPS явно сохраняйте legacy (это также default). `OUTPUT=json` даёт подробный локальный результат; session mode по умолчанию показывает краткую сводку.

Новые каталоги:

| Команда | Каталог |
|---|---|
| fetch-backup INSTANCE=acceptance | ~/simple-budget-backups/acceptance/ |
| fetch-backup INSTANCE=production | ~/simple-budget-backups/production/ |
| legacy-export (только при отдельно разрешённой остановке источника) | ~/simple-budget-backups/migration/transfer-ID/ |
| pki-backup | ~/simple-budget-backups/pki/ |

Fetch сохраняет полный backup ID, проверяет SHA256/native verify/installation ID до публикации. Идентичный файл не перезаписывается, отличается — отказ. Старые копии не переносятся и не удаляются.

```sh
make pki-backup ENV=production INSTANCE=acceptance
# Необязательный другой НОВЫЙ путь в приватном каталоге:
make pki-backup ENV=production INSTANCE=acceptance PKI_ARCHIVE="$HOME/simple-budget-backups/pki/extra.dmg"
```

Имя по умолчанию pki-YYYY-MM-DD_HH-mm-ss.dmg, время Europe/Moscow; коллизии получают числовой suffix. Registry должен быть экспортирован меньше24часов назад; при просрочке сначала `make pki-export ENV=production INSTANCE=acceptance`. AES-256, пароль через NUL-terminated stdinpass, проверка содержимого при readonly mount и detach до публикации. В ручном режиме recovery-пароль вводится дважды; дальнейшие проверки используют его только в памяти команды. Храните пароль независимо от этого Mac, архив — также на независимом носителе.

Пароль браузера сохраняется только средствами браузера/его менеджера; CLI Keychain не настраивает Chrome/Android. Возможность биометрического autofill именно HTTP Basic dialog требует пользовательской проверки; WebAuthn/passkeys и серверный auth не менялись.

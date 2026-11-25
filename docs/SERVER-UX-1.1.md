# Server UX 1.1 — Touch ID, Keychain & Server Manager

25 ноября 2026. Изменения реализованы и проверены локально. VPS, Caddy, SSH, sudoers, firewall, acceptance и её таймеры не изменялись. Настоящие данные и ключи не использовались в автоматических тестах. Перенос данных не выполнялся.

## Результат и границы

Добавлено русское меню `make server`, стандартные приватные каталоги копий, проверяемый автоматический PKI DMG и опциональная ограниченная административная сессия с одним запуском sudo. Существующие команды и серверные протоколы backup/deploy/recovery сохранены.

**Настоящее биометрическое хранилище на текущей сборке недоступно.** Нативный Swift helper подтвердил наличие доступного Touch ID, но Data Protection Keychain вернул `-34018` (errSecMissingEntitlement). Команды не сохраняют секреты обходным способом, не читают обычный login Keychain вместо защищённого и не показывают собственный «Touch ID». Рабочий режим на этом Mac — `AUTH=manual`.

Код доступа к Security Framework и LAContext реализован с проверкой возможностей, но успешные регистрация/получение/удаление настоящего биометрического item на этом Mac **не подтверждены**. Mock-тесты проверяют поведение клиента, не работоспособность биометрии. Нет оснований обещать одно касание для нескольких секретов.

## Фактическая совместимость

- macOS 12.7.6, build21H1320, Intel x86_64.
- Command Line Tools: Apple Swift5.7.2; Security и LocalAuthentication доступны.
- `LAContext.canEvaluatePolicy(deviceOwnerAuthenticationWithBiometrics)` — true; biometryType Touch ID.
- Нативный синтетический неперсональный DP Keychain probe — `-34018`, и до, и после ad-hoc hardened-runtime подписи. Никакой настоящий секрет в Keychain не запрашивался.
- Установленный Chrome150.0.7871.125. Версия прочитана из Info.plist, профиль и сохранённые пароли не извлекались.

Apple различает file-based/login Keychain с ACL и Data Protection Keychain с access groups и SecAccessControl. Для DP entitlement требуется разрешение provisioning profile; CLI также нуждается в подходящей упаковке для профиля. Ad-hoc codesign не предоставляет это разрешение. Платная учётная запись или профиль у пользователя не предполагались, identities/profiles из личного Keychain не извлекались. См. [Apple TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains), [TN3125](https://developer.apple.com/documentation/technotes/tn3125-inside-code-signing-provisioning-profiles), [SecAccessControl flags](https://developer.apple.com/documentation/security/secaccesscontrolcreateflags).

Для будущего включения DP storage потребуется отдельно подготовленный подписанный и provisioned helper/app bundle, разрешённая access group, проверка изоляции от других приложений и пользовательская приёмка биометрии. Текущая команда сборки этого не делает. Не добавлять неподтверждённые entitlements и не отключать SIP/AMFI ради запуска.

## Секреты и контекст одной операции

`AUTH=manual` — значение по умолчанию. `AUTH=keychain` — явный запрос к DP helper; при отсутствии возможностей, locked/missing item или отмене операция останавливается. Скрытого перехода к сохранённым данным или ручному способу нет; пользователь может явно повторить команду с `AUTH=manual`.

Область item: `171.22.237.16/INSTANCE/INSTALLATION_ID/KIND/DEVICE`. KIND: app, client, pki. Для app/pki последний компонент default; для client — имя удостоверения. Даже общий клиентский ключ имеет отдельную запись для выбранной установки. Acceptance пароль не используется для production, новый installation ID не получает секрет прежней установки автоматически.

Будущий доступный helper:

- DP Keychain, `WhenUnlockedThisDeviceOnly` и `biometryCurrentSet` (изменение набора отпечатков требует повторной регистрации).
- Нативная biometric-only evaluatePolicy перед первым секретом процесса, затем один LAContext до конца команды. ОС вправе запросить дополнительные подтверждения при доступе к items; реальное число пока неизвестно.
- Передача только через унаследованный socketpair FD; секретов нет в argv/environment/stdout/log. Стандартный stdout helper используется только для несекретного status.
- Ограниченный протокол put/get/delete, одна установка, не более24 запросов, 15-минутный срок, таймаут чтения. Не daemon и не глобальный агент авторизации.
- Удаление тоже требует биометрической оценки. Замена использует SecItemUpdate, без предварительного удаления старого item.
- Перед deploy/restore/rollback/recover локальные секреты запрашиваются до изменения сервера; отказ или отмена не вводят сервис в maintenance. На время команды значения хранятся в памяти Operation, не между пунктами меню. Python закрывает helper и очищает ссылки на cache при успехе/ошибке/отмене. Строки находятся в памяти процесса команды; гарантированное стирание всех копий из памяти Python/Swift не заявляется. Расшифрованные private key файлы не создаются.

`SECRET=app|client|pki`, DEVICE и INSTANCE выбираются явно. Регистрация app/pki: минимум12 символов, рекомендуется16 и больше; максимум4096 UTF-8 bytes. CA passphrase всегда остаётся ручным; sudo password никогда не сохраняется.

## SSH и sudo

Текущий alias budget, budgetadmin, ED25519, identitiesonly=yes и addkeystoagent=true сохранены. Все транспортные вызовы сохраняют StrictHostKeyChecking=yes; ключи и SSH-конфигурация не менялись. Штатный ssh-agent/login Keychain может уменьшать повторный ввод SSH passphrase, но это **не** Touch ID на каждое использование ключа. Биометрическая подпись существующим файловым ED25519 ключом не добавлена. Hardware/security-key/сторонний агент потребовали бы отдельного решения и возможной замены ключа; сейчас не устанавливаются.

`SUDO_MODE=legacy` оставлен по умолчанию: работает с уже установленным серверным wrapper. `SUDO_MODE=session` требует нового admin_session.py и версии протокола1 на VPS. Неизвестная версия/сеть не приводят к скрытому повтору операции старым способом.

Сессия:

1. Read-only проверка протокола.
2. Один `ssh -tt … sudo budget-admin session INSTANCE TOKEN`; пароль вручную вводит пользователь.
3. Строгий allowlist действий для исходной операции, фиксированная установка/ID, возрастающий sequence, не более32 команд. Один изменяющий deployment-переход; commit только для принадлежащего сессии request ID, включая исходный ID recover.
4. Каждый шаг вызывает существующий `/usr/local/sbin/budget-admin`; тот сохраняет прежние проверки, operations lock, journal, migration и HTTPS proof. Broker не держит operations lock поверх дочернего handler.
5. Idle limit120s; новые команды прекращаются через30мин; уже начатый handler ограничен45мин. Ошибка завершает сессию, повтор mutation не выполняется. Закрытие/разрыв не удаляет deployment/recovery evidence.
6. Удалённый stdin имеет отдельный локальный PTY. Пользовательский терминал подключается только для начального sudo и интерактивной initialize. Во время локального ввода app/client passphrase SSH его не читает. Poll/transfer соединения не выполняют sudo.

Это ограниченный привилегированный процесс одной операции, а не изменение sudo timestamp или бессрочный root shell. Нет NOPASSWD, sudoers изменений, пароля sudo в stdin из хранилища или фонового `sudo -v`. Между пунктами меню сессии/секреты не сохраняются. В mailbox остаётся маленький несекретный reply для диагностики; произвольные старые каталоги не удаляются.

### Число запросов

| Сценарий | На принятом VPS до этого изменения | Новый локальный контракт; VPS ещё не проверен |
|---|---|---|
| Deploy без ошибок | Обычно3 sudo: prepare, commit, cleanup;1 client-key passphrase;1 app password | Один запуск sudo в session mode; те же2 ручных локальных секрета при AUTH=manual |
| Status | Обычно2 sudo | Один запуск sudo |
| Fetch latest | До3 sudo | Один запуск sudo |
| PKI backup | Путь плюс два ввода recovery password | Путь автоматический, два ручных ввода; create/verify/attach используют тот же пароль в памяти |
| Touch ID | Не используется текущей сборкой | Не измерено: DP storage недоступен |

Штатный sudo может не спросить пароль из-за своего действующего контекста либо повторить при ошибке ввода; «один запуск sudo» не обещает ровно один prompt при любых обстоятельствах. На VPS новый режим пока **не установлен**, число реальных prompts не выдаётся за измеренное. При HTTP401 сохранены максимум3 попытки app password без повторного deployment; после неверного сохранённого app password следующие попытки вводятся явно вручную, Keychain не перезаписывается автоматически.

## Локальные архивы

- `~/simple-budget-backups/acceptance/<полный-backup-ID>.tar.gz`
- `~/simple-budget-backups/production/<полный-backup-ID>.tar.gz`
- `~/simple-budget-backups/migration/transfer-<installation-ID>/…` — отдельный CLI repository экспорта с прежним manifest/recovery-контрактом.
- `~/simple-budget-backups/pki/pki-YYYY-MM-DD_HH-mm-ss.dmg` — **Europe/Moscow** независимо от TZ Mac. Коллизии: `-1`, `-2` и т.д.; публикация всегда no-replace.

Новые подкаталоги0700, файлы0600. Существующий родитель simple-budget-backups0755 допускается только при собственном владельце и отсутствии group/world-write; его права и старые файлы не меняются. Пользовательские symlink, hardlink, чужие/небезопасные каталоги и размещение внутри репозитория отвергаются. `PKI_ARCHIVE=/absolute/private/new.dmg` позволяет выбрать другой приватный каталог.

Fetch сначала скачивает во временный приватный staging, проверяет серверный SHA256, native verify, backup ID и installation ID, затем публикует без замены. Идентичная проверенная копия считается успешным повтором; другая копия с тем же именем не заменяется. Прежние файлы из `.simple-budget-admin` не переносятся и не удаляются. Вспомогательные старые acceptance-download receipts сохраняют исторические пути; стандартная пользовательская команда fetch-backup использует новые каталоги.

PKI backup требует свежий (<24h, не из будущего), приватный regular registry, известные allowed/revoked states и SHA fingerprints; источник — существующий `pki-export` через проверенный SSH. Незавершённый отзыв экспортировать нельзя. PEM keys должны быть зашифрованы; PKCS#12 с пустым паролем отвергается. CA/клиентские ключи не расшифровываются в staging.

hdiutil получает NUL-terminated пароль через `-stdinpass`, не через arguments/environment. Последовательность: AES-256 create UDZO → verify → readonly/nobrowse mount → сверка SHA всех исходных файлов/registry → detach → атомарная no-replace публикация. Если проверка/отключение не удались, готового архива не появляется. Учитывается канонический системный alias /var→/private/var. Прерывание обычной команды очищает временный staging; SIGKILL/потеря питания могут оставить приватный неопубликованный staging, который требует осмотра, а не произвольного удаления.

**Recovery-пароль хранить отдельно от этого Mac**, например в доступном с другого устройства надёжном менеджере паролей или запечатанной бумажной записи. Наличие секрета только в локальном Keychain не обеспечивает восстановление после потери Mac. DMG надо скопировать на независимый носитель. App backups не содержат PKI.

## Server Manager и вывод

`make server` всегда начинает с acceptance, даже если окружение содержит INSTANCE=production. На экране сервер/IP, instance, installation ID, AUTH/SUDO_MODE. Все действия вызывают прежний mac.py в отдельном процессе. Меню не содержит второго deployment/recovery ядра.

Есть11 запрошенных разделов, дополнительно12 — явное переключение установки,13 — штатный recovery. Для production нужно напечатать PRODUCTION; restore/rollback/revoke сохраняют собственные подтверждения с целью и последствиями. При локальном pending меню блокирует новый deploy/restore/rollback и предлагает status/recover; серверный journal дополнительно проверяет wrapper. Ctrl+C завершает меню с130, EOF выходит; неверный ввод/сеть/ненулевой exit показываются и возвращают пользователя к меню. Внутри раздела11 можно выбрать ручной/Keychain режим на текущий запуск меню.

В session mode выводятся этапы handler, summary status/backup/deploy и пути проверенных копий. `OUTPUT=json` возвращает подробный JSON. Старый сервер в legacy mode по-прежнему сам печатает подробный JSON; скрывать его до согласованного обновления wrapper не стали.

## Пароль браузера

Basic Auth/mTLS/Host/Origin/rate limiting не менялись. Это HTTP authentication dialog, не новая HTML login-форма. Google описывает сохранение паролей и опциональную биометрическую защиту autofill, Apple — iCloud Keychain/AutoFill; эти общие возможности не доказывают одинаковое поведение HTTP Basic Auth на всех версиях браузера: [Google Chrome](https://support.google.com/chrome/answer/95606), [Apple](https://support.apple.com/guide/passwords/icloud-keychain-mchlf375f392/mac).

Из прежней приёмки реально подтверждены вход Chrome/Mac и Android, работа после reboot, устранение мешавшего VPN-расширения. Не проверены в этом этапе предложение Save для HTTP Basic, сохранение между перезапусками браузера и число biometric prompts Google Password Manager на OnePlus. Без действий пользователя с настоящим паролем эти пункты не объявляются пройденными.

Проверка пользователем: войти обычным профилем на https://171.22.237.16 как budget, принять предложение браузера сохранить пароль, если оно появляется; перезапустить браузер и отметить запросы cert/password/биометрии. На Android повторить с используемым менеджером паролей. Не вводить пароль в URL. Если менеджер не поддерживает этот dialog — оставлять ручной ввод; не менять сервер ради autofill. WebAuthn/passkeys/PWA sessions не добавлялись и не обещаются.

## Проверки

- Python ops:110/110 PASS на Python3.12.2 и системном /usr/bin/python3 версии3.9.6, включая существующие контракты installer/Caddy/backup/deploy и новые scope/cancel/fallback, секреты в process arguments/env, private socket, session allowlist/identity/replay/close/timeout, pending preservation, private PTY, paths/collisions/link guards, DMG failures, menu EOF/Ctrl+C/production confirmation.
- Native Go CLI integration PASS: init, backup, candidate restore/migrate, live migration, pre-open rollback, сохранение old data, integrity/schema; OS/systemd/proxy в этой проверке подменены.
- Реальный macOS hdiutil на синтетических зашифрованных CA-файлах: create/verify/open/content match/detach PASS, encrypted=true, неверный пароль отвергнут, перезапись запрещена, исходный key hash неизменен, подключённого образа не осталось.
- Swift компилируется с deployment target macOS12.0, ad-hoc hardened runtime. Capabilities измерены нативно: Touch ID true, DP status -34018. Реальный биометрический unlock/store НЕ PASS.
- py_compile и git diff --check PASS. Go/frontend исходники не менялись; полный Go/race/Playwright не повторялся. Предыдущая регрессионная база остаётся применимой к тем же исходникам приложения.
- Новая root session на настоящем Linux/SSH/TTY/sudo пока НЕ проверена. Локальная проверка вызывает существующий handler через синтетический run, подтверждает отсутствие внешнего lock и сохранение journals. Нативный PTY-тест подтверждает отсутствие передачи локального секрета в idle SSH канал.

## Начало использования и дальнейшая приёмка

Сейчас, без изменений VPS:

```sh
cd "$HOME/Desktop/simple-budget-app"
make keychain-build
make keychain-status INSTANCE=acceptance
make server AUTH=manual SUDO_MODE=legacy
# Автоматическое имя и проверка PKI DMG, только когда registry ещё свежий:
make pki-backup ENV=production INSTANCE=acceptance AUTH=manual
```

На текущей сборке после status -34018 не нужно вводить секреты для регистрации. Когда будет доступен отдельно проверенный provisioned helper, интерфейс регистрации такой:

```sh
make keychain-register INSTANCE=acceptance SECRET=app
make keychain-register INSTANCE=acceptance SECRET=client DEVICE=mac-1
make keychain-register INSTANCE=acceptance SECRET=pki
make keychain-check INSTANCE=acceptance SECRET=app
make keychain-delete INSTANCE=acceptance SECRET=app
# production регистрируется отдельно и только при существующем installation ID.
```

Для новой sudo-сессии требуется отдельное разрешение пользователя на обновление административных компонентов VPS. После разрешения, в таком порядке:

```sh
make server-install ENV=production
make status ENV=production INSTANCE=acceptance SUDO_MODE=session
make fetch-backup ENV=production INSTANCE=acceptance SUDO_MODE=session
make server AUTH=manual SUDO_MODE=session
```

Не запускать installer в рамках этого локального этапа. Он обновит admin_session.py/remote.py и manifest инфраструктуры, а не рабочий Go release или данные. Перед приёмкой обновления сравнить Caddy config/registry/storage, units/timers и installed release, после — подтвердить неизменность и фактическое количество запросов sudo. Затем отдельный synthetic deploy/recover с ручным HTTPS proof; отмена/обрыв должны сохранить journal. Не проводить непрошедшую session-проверку на настоящих данных.

Ожидаемые nightly backup и automatic HTTPS renewal из Server Deployment продолжают наблюдаться независимо от Mac/Keychain. Ни график, ни серверная retention7daily/2ordinary-pre-update/pins не менялись.

## Подготовленный чистый release

Исходники Server UX зафиксированы локальным commit `a47379e901acfa0db0e492ed7cd67ffb8358fd6a`, push не выполнялся. Собран `build/releases/a47379e901ac-20261125T154743Z`: Linux amd64, Go1.27.4, modified=false, embedded_frontend=true, schema14, размер15552672B. SHA256 ELF: `1cdd370077e644bfaf3f018d6dd5f066c50c6719dc9b3cb3fdc564285d047465`; независимо сверён с release.json. Binary govulncheck этого ELF: PASS, No vulnerabilities found. Это кросс-сборка; новый ELF на VPS не запускался. Административные Python-компоненты поставляются installer отдельно; для session-проверки замена уже работающего Go-бинарника не требуется.

Следующий шаг требует отдельного разрешения на обновление административных компонентов acceptance и проверку sudo-сессии. До него текущий VPS работает на прежнем release, SUDO_MODE=legacy сохраняет совместимость.

## Подготовка фактической sudo-приёмки перед Live Sync & Web Auth

Перед началом следующего этапа read-only SSH подтвердил: acceptance и Caddy active, daily next26ноября00:30UTC, TLS timer active. Старый wrapper ещё не поддерживает session-protocol. Никаких серверных изменений этим чтением не выполнено.

В локальном broker устранён дефект обработки результата client-list: JSON-массив больше не рассматривается как объект с полем result. Добавлена проверка client-list → clean-incoming → close; весь ops-набор111/111 PASS. Это исправление нужно включить в административную поставку до серверной приёмки.

Приёмка sudo-сессии остаётся первым шагом нового задания; HTML Auth и Live Sync не считаются реализованными. Обновление административных компонентов требует отдельного разрешения по пункту13 нового ТЗ и ручного sudo в терминале пользователя.

# PROD / STG

PROD: `https://171.22.237.16/` → `127.0.0.1:3001`.
STG: `https://171.22.237.16:8443/` → `127.0.0.1:3002`.
STG использует прежнюю `INSTANCE=acceptance`, её пользователя, каталоги и units.
Настоящие данные в STG запрещены.

## Однократная установка

Сначала пользователь коммитит проверенные изменения. Затем требуется отдельное
разрешение на VPS, Caddy и открытие TCP 8443. До него команды ниже не выполнять.
Новый CLI требует административный protocol v2: сначала `server-install`.
Несовместимость обнаруживается до загрузки изменения и создания pending journal.

```sh
make server-install ENV=production
make stg-setup ENV=production INSTANCE=acceptance SUDO_MODE=session AUTH=manual
make deploy ENV=production INSTANCE=acceptance DEVICE=mac-1 SUDO_MODE=session AUTH=manual
make stg-reset ENV=production INSTANCE=acceptance DEVICE=mac-1 SUDO_MODE=session AUTH=manual
```

`stg-setup` сохраняет ID, данные и отдельный пароль acceptance. Если он совпадает
с PROD, требует новый пароль STG в терминале. Однократно перезапускает Caddy
(активные HTTPS/SSE соединения переподключатся), открывает 8443, оставляет STG
остановленной. Повтор setup после обрыва идемпотентен; при pending journal сначала
`status` / `recover`, не повторный импорт. Старый релиз без поддержки изоляции
cookies запускаться на STG не будет; нужен указанный `deploy`. Откат STG к такому
старому релизу также отклоняется до остановки сервиса и восстановления данных.

Перед изменением Caddy `stg-setup` проверяет текущий маршрут PROD и сохраняет его
точную конфигурацию, drop-in и метаданные в root-only `stg-setup-recovery.json`.
При ошибке применения восстанавливает прежний Caddy, оставляя STG остановленной.
После прерывания (`stg_setup_state=preparing/recovering`) сначала выполните
`make stg-setup ENV=production INSTANCE=acceptance SUDO_MODE=session AUTH=manual DECISION=rollback`,
затем повторите setup. Эта команда восстанавливает конфигурацию прокси, не данные.
Изменившиеся registry или конфигурация блокируют слепое восстановление.
Правило TCP 8443 сохраняется: оно могло существовать ранее, но в восстановленном
Caddy listener 8443 отсутствует. Успешный setup требует один restart Caddy;
восстановление после ошибки требует дополнительный restart. PROD-сервис не перезапускается.

`stg-reset` с подтверждением создаёт синтетические проекты, задачи/подзадачи,
выполненные и отменённые задачи, сессию, шаблон и повторения. Использует штатный
backup/restore и recovery; прежний набор сохраняется в pre-update копии.
Финансы пустые. Новый набор открывается после обычного HTTPS proof.

## Ежедневное управление

`make budget`: PROD и `SUDO_MODE=session` по умолчанию. Заголовок получает реальные
состояния/короткие SHA без sudo. Пункт 12 выбирает окружение; 14 запускает/останавливает
STG по наблюдаемому состоянию, 15 сбрасывает её данные, 16 устанавливает в PROD
ровно текущий проверенный бинарник STG. SHA-256 проверяется до установки; пересборки
при продвижении нет. Проверку STG пользователь подтверждает явно.

Запуск/остановка сохраняют release и данные, не перезапускают Caddy/PROD.
STG не включена в автозапуск; её daily timer выключен даже после deployment.
Лимиты: MemoryHigh 128 MiB, MemoryMax 192 MiB, swap 64 MiB, CPUWeight 20, Nice 10.
Штатные pre-update копии остаются; автоматических скачиваний STG нет.
`VERBOSE=1` включает технический вывод, `OUTPUT=json` — подробный результат.
`NO_COLOR=1` отключает цвета. Прямые изменяющие Make-команды сохраняют прежние
безопасные defaults (acceptance/legacy); для одной sudo-сессии укажите session.

## Изоляция и приёмка

Порты не разделяют cookies. Caddy передаёт backend только его session cookie,
нормализует имена всех Set-Cookie к имени этого окружения и удаляет Clear-Site-Data.
Используются `__Host-budget-session` / `__Host-budget-stg-session`, HttpOnly/Secure.
Оба listener требуют существующий mTLS allowlist. ACME storage и сертификат общие.
IP-клиенты могут не передавать SNI: проверка Host выполняется отдельно, с портом
в backend. Fetch Metadata блокирует cross-origin API даже для `same-site`;
обычная GET-навигация к оболочке/логину разрешена. CSRF/Origin остаются обязательными.
Это изоляция приложений на общем VPS, не контейнер для недоверенного root-кода.

Локальные проверки используют только временные синтетические данные и PKI:
`make test-ops`, `make test-ops-native`, Go/race/vet, frontend/Playwright,
`CADDY_BIN=/path/to/caddy make test-stg-proxy`. Последняя запускает Caddy 2.11.9
на loopback: mTLS, cookies, SSE, gate, Chromium localStorage/service worker.
Для временного self-signed сертификата только этот тестовый браузер игнорирует
ошибки доверия; mTLS отдельно проверяется Python-клиентом с проверкой CA.

Локально прошли 162 ops-теста, native CLI, 183 браузерных сценария (включая
повторные прогоны), frontend, Go/vet, Linux-сборка и source/binary govulncheck.
Race основного пакета прошёл; общий прогон планировщика достиг лимита 30 минут
после 152 тестов. Оставшиеся 19 прошли отдельно; сообщений о гонках нет.
Единый непрерывный race-прогон всех пакетов не завершился.

После разрешённой установки на VPS:
1. `make status ENV=production INSTANCE=acceptance SUDO_MODE=session OUTPUT=json`:
   `staging_isolation.passed=true`, disabled autostart/daily, ресурсные лимиты.
   Проверка выполняет read/write/traverse probes от реального пользователя STG
   против каталогов данных, credentials и backups PROD; содержимое не читается.
2. На Mac и OnePlus открыть оба URL: отдельные пароли/сессии, жёлтая метка только
   STG. На STG проверить два клиента, SSE, черновик и переподключение.
3. Пункт 14 stop/start, затем обновление STG: PROD остаётся доступен; PID Caddy и
   PROD не меняются, STG возвращается с прежним release/данными. После reboot STG
   остаётся выключенной. Перезагрузку выполнять только с отдельного разрешения.
4. Снять `status OUTPUT=json` обоих окружений: `service_resources` содержит RAM,
   peak, накопленный CPUUsageNSec и TasksCurrent. Сравнить до/после нагрузки STG
   и повторных SSE-подключений. Эти VPS-замеры и мобильная проверка ещё не выполнены.

PROD backup timer и единый TLS monitor не меняются. На PROD не запускать
синтетические acceptance-тесты. Старый `live_acceptance.py` привязан к историческому
release и должен отказывать новому; его ограничения не снимать ради запуска.

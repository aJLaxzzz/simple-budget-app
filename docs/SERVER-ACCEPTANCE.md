# VPS-приёмка после этапа 3

Это чек-лист полной приёмки; подробные фактические результаты — в [журнале VPS](SERVER-DEPLOYMENT-ACCEPTANCE.md). Приёмка выполняется по разрешению пользователя на INSTANCE=acceptance, без настоящих данных. Непроверенные части составных пунктов ниже остаются открытыми. Второй Mac можно подключить позднее.

Промежуточные подтверждения 25 ноября 2026:

- [x] Синтетическая установка запущена на Linux, release6233199, schema14, отдельные user/ID/live/loopback3002.
- [x] Реальный LoadCredential и authenticated HTTPS commit после исправления ACL.
- [x] HTTPS GET/POST, проверки пароля/Host/Origin, отказ без client certificate.
- [x] Manual backup, HTTPS download и независимый verify на Mac.
- [x] Full restore дошёл до commit; после потери SSH восстановлена та же административная операция. Детальная сверка данных ещё впереди.
- [x] Update6233199→379bc1c и explicit rollback→6233199 завершились committed на VPS; app/Caddy active, NRestarts0. Сверка данных и защищённых поколений ещё впереди.
- [x] acceptance-device выпущен, добавлен и отозван; CLI probe завершился успешно, mac-1/phone остались allowed. Дополнительная строгая матрица TLS1.2/1.3 × h1/h2 позднее пройдена с acceptance-tls-device; см. ниже.
- [x] После отзыва тестового устройства пользователь вошёл с Mac и телефона; создание и завершение задачи синхронизируются после обновления страницы.
- [x] hdiutil создал зашифрованную PKI-копию на Mac; пользователь открыл её паролем, подтвердил ключи и актуальный registry (mac-1/phone allowed, acceptance-device revoked), затем извлёк образ. Дополнительный независимый носитель и полное восстановление PKI ещё не проверены.
- [x] На VPS: UID-права, parent/SQLite flock, отказ maintenance при открытом live, production startup guards, полноценный backup controller.
- [x] Изолированный Linux стенд:100проектов/1000задач,20серий, повреждение/restore с полной сверкой строк и финансовых bytes, restart, отказ малого tmpfs.
- [x] После согласованного reboot новый boot ID; SSH/Caddy/app автоматически active, failed units0, timers enabled, backend loopback3002, без client cert TLS отказ и без app password401. Пользователь подтвердил сохранение задач и браузерный доступ после reboot. Конфликт VPN-расширения обычного Chrome устранён на Mac.
- [x] Таймеры установлены, daily запланирован на03:30МСК. Первый автоматический daily и renewal ещё не подтверждены.

- [x] После reboot acceptance-http:28 HTTP-проверок, два Range206 полного архива с SHA/native verify, planner download с integrity/FK, исходный task/project ID сохранён. Root status без pending journals, proxy open.
- [x] Новый pki-20261125-final.dmg создан; пользователь подтвердил registry: mac-1/phone allowed, оба временных удостоверения revoked. Независимый носитель и полное восстановление PKI отдельно не проверены.

## До установки

- [ ] Чистый проверенный commit; release modified=false; сверены commit/schema14/SHA-256. Личных файлов в release нет.
- [ ] SSH alias и host key проверены. UFW сохраняет22/80/443TCP;3001/3002/2019 снаружи недоступны, UDP443 не нужен.
- [ ] Реальный Caddy2.11.9/Caddyfile сверены. Все TLS policies, включая default: require_and_verify, правильная CA и точный leaf allowlist. HTTP80 обслуживает только ACME/404.
- [ ] Записаны текущие certificate serial/SAN/issuer/срок. ACME storage сохранён; новый account или forced renewal не создавались.
- [ ] Измерены RAM/swap/disk/inodes. Есть резерв max(2GiB,15%) плюс временное пространство операции.

## Linux runtime и права

- [ ] Выполнены начальные команды из [SERVER-OPERATIONS](SERVER-OPERATIONS.md). Acceptance имеет отдельные OS user, ID, data и loopback3002.
- [ ] ELF запускается без Git/Node/frontend directory. Production отказывает при отсутствии пароля/marker, неверных ID/schema и symlink paths; пустая база молча не создаётся.
- [ ] LoadCredential работает; пароль отсутствует в env/journal/ps. budget не имеет sudo и прав записи в release/admin/PKI.
- [ ] Web может менять только live и читать готовые backup. Staging/credentials недоступны; удалить backup/release нельзя. Acceptance не читает production.
- [ ] SQLite WAL/flock/parent lease работают: второй Store и maintenance при работающем приложении отказывают. WAL корректно читается после перезапуска.
- [ ] Pending migration/restore journal блокирует запуск. SIGTERM штатно останавливает процесс. Права DB/WAL/SHM/lock сохраняются после root backup и migration.
- [ ] `/api/ready` отражает активные commit/schema; пароль обязателен даже на loopback. Прямой внешний доступ к Go отсутствует.

## Caddy и устройства

- [ ] JSON применён после caddy validate. Все mTLS policies сохранены, admin API выключен, ACME lifecycle работает без него.
- [ ] HTTPS по IP доверенный, в том числе без DNS SNI. Host, Authorization, Origin и Content-Type передаются до Go без подмены.
- [ ] Без сертификата, с неизвестным, просроченным или CA-signed-but-not-allowlisted сертификатом TLS отказывает. Действующий cert без app password получает401. Чужой Host/Origin и forwarded bypass отвергаются.
- [ ] GET/POST/static/backup download работают с Mac и OnePlus13 через Wi-Fi и мобильную сеть, с VPN и без. Медленное скачивание не требует открытого backend.
- [x] Временные acceptance-device и acceptance-tls-device выпущены/добавлены/отозваны. Четыре h1/h2 TLS1.2/1.3 канала закрылись; шесть новых/session-state попыток получили BAD_CERTIFICATE. TLS1.3 ticket не выдавался, реальный ticket replay неприменим. mac-1/phone сохранены в allowlist; их работа подтверждена пользователем после второго отзыва и reboot.
- [ ] Ошибка применения отзыва оставляет Caddy остановленным. Старый configuration backup не возвращает отозванный leaf.
- [x] Отсутствие session tickets наблюдалось на реальном сервере для TLS1.2/1.3 с HTTP/1.1/2 после ответов; TLS1.3 session events не получены. Конечное наблюдение не является утверждением о бесконечном времени.
- [ ] Encrypted PKI DMG содержит актуальные allowed/revoked registry и зашифрованные ключи; хранится отдельно от Git и app backup.
- [ ] Второй Mac проверен при подключении. Установленный PWA проверяется отдельно при его появлении; сейчас этот сценарий не подтверждён.
- [ ] Hourly TLS monitor видит реально обслуживаемый leaf и проверяет trusted chain/IP. Срабатывают warning<48h и critical<24h.
- [ ] Первое автоматическое renewal подтверждено новым serial и успешным событием, без forced renewal, удаления cert или изменения часов. Пользователь планировал проверку29ноября.

## Backup, restore, update

- [ ] Daily timer запускается в03:30 Москвы, Persistent включён; UI показывает реальный enabled/next_at. Устаревание после26часов. Повторный запуск за ту же московскую дату не дублирует daily.
- [ ] Manual backup выполняет stop → snapshot/verify → start → compress/verify → publish. Короткая остановка измерена. Архив скачан на Mac и повторно проверен.
- [ ] Full restore на synthetic data сохраняет ID, историю, серии, календарь, иконки и JSON. Повреждённая SQLite восстанавливается без обычного запуска Store; прежний каталог сохранён.
- [ ] После8+ успешных daily остаётся7; обычных pre-update не более2; pins защищены. Неудачный backup не удаляет исправные копии.
- [ ] Нехватка диска/inode проверена на отдельном изолированном томе, без заполнения корневого диска VPS.
- [ ] Update двух чистых release проходит journal, protected backup, candidate migration, authenticated readiness и Mac HTTPS до открытия записей.
- [ ] Обрыв SSH/ошибка trial до открытия доступа разрешается explicit recovery/rollback. Данные и release согласованы. После opening автоматический data rollback запрещён.
- [ ] Потерянный ответ commit не создаёт вторую операцию. Explicit rollback после commit предупреждает потерю новых записей и сохраняет свежую protected копию.
- [ ] Reboot сохраняет units/timers/mTLS/app auth. Pending journal не позволяет запустить несогласованный live.
- [ ] Cleanup не удаляет копии pending/rollback, последнее исправное поколение или неизвестные данные.

## Ресурсы и настоящий перенос

- [ ] Измерены RSS приложения/Caddy, peak backup/compression/restore, swap, CPU/load, диск до/после, время startup/backup/update. Нет OOM и restart loop. При необходимости лимиты согласованно уточнены.
- [ ] Acceptance и её timer остановлены перед production import: на864MiB не остаются два постоянно нагруженных приложения.
- [ ] Источник legacy данных установлен точно; остановка Mac разрешена отдельно; оригинальные bytes не меняются, полный архив сохранён на Mac.
- [ ] После import сверены количества и ID задач/проектов/серий, история, периоды работы, иконки, категории и финансовые записи; проверен календарь.
- [ ] VPS — единственный источник записей. Локальная разработка использует только test-marked данные. Скачана независимая внешняя копия.

У каждой отметки сохранить дату, release SHA, installation ID, команду/exit и обезличенный результат. Не прикладывать ключи, пароль, реальные JSON/SQLite или персональные ответы API.

# Целевая проверка Beta Update 3

Все браузерные сценарии запускаются с настоящим Go API. Базовый
`playwright.config.cjs` создаёт отдельный временный каталог `budget-planner-ui-*`;
`browser-server.cjs` строит отдельный бинарник и задаёт ему этот каталог данных.
Рабочая SQLite и финансовые JSON не используются.

```sh
npx playwright test tests/browser/beta3.spec.cjs
npx playwright test -c tests/beta-update-3/playwright.config.cjs
```

Конфигурация снимков использует Europe/Moscow, ru-RU, тёмную тему, 1440×900 и
390×900. Контрольные задачи и периоды заданы внутри фикстур. Анимации отключаются
при снимке. Неожиданные `pageerror` проверяются общим browser fixture.

Снимки «до» получены с `efd71cd` в отдельном временном checkout через `git archive`.
В него были скопированы только эти capture-скрипты и подключён тестовый
`node_modules`. Для исходной версии задавался `BETA3_BEFORE=1`; для сценариев уборки
также `BETA3_OUTPUT` — абсолютный путь к `artifacts/beta-update-3` текущей работы.
Сценарий дней рождения в старой копии пишет в её локальный `before/`; эти два
оригинальных PNG следует перенести в основной `before/` без изменения размеров.
Не запускайте старую версию с рабочим каталогом данных.

`package.py` создаёт пары, контактные листы, локальную галерею, манифест и ZIP.
Требуется Python с Pillow и Arial Unicode (macOS) либо DejaVu Sans (Linux).

```sh
python3 tests/beta-update-3/package.py
```

В ZIP включается только явный список PNG, галерея, манифест и технический отчёт.
Тестовые базы, логи, бинарники, baseline-хеши и пути временных checkout в архив
не включаются. `artifacts/` и `test-results/` исключены `.gitignore`.

Для полной проверки race detector на машине, где единый planner-процесс
превышает 30 минут, доступен последовательный запуск всех тестов двумя группами:

```sh
python3 tests/beta-update-3/race-check.py --list-only
python3 tests/beta-update-3/race-check.py
```

Список получается через `go test -list .`; проверяется отсутствие пропусков и
пересечения групп. Каждый тест запускается с `-race -count=1 -timeout=30m`.
Стандартный Go-прогон, frontend unit и полный Playwright остаются отдельными
проверками и не заменяются целевой генерацией снимков.

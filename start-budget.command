#!/bin/bash
# Локальный запуск. Закрытие браузера не останавливает сервер.
cd "$(dirname "$0")" || exit 1

listen_addr="${BUDGET_ADDR:-127.0.0.1:3001}"
browser_addr="$listen_addr"
case "$listen_addr" in
  0.0.0.0:*|\[::\]:*) browser_addr="${BUDGET_ALLOWED_HOSTS%%,*}" ;;
esac
scheme=http
if [ -n "$BUDGET_TLS_CERT" ]; then scheme=https; fi
url="$scheme://$browser_addr"

is_budget_running() {
  curl --silent --show-error --head --connect-timeout 1 --max-time 2 "$url/" 2>/dev/null |
    grep -qi '^X-Budget-App: simple-budget-app'
}

if is_budget_running; then
  open "$url"
  exit 0
fi

./budget &
BID=$!
stop_budget() {
  kill -TERM "$BID" 2>/dev/null
  wait "$BID"
  exit 0
}
trap stop_budget INT TERM

for attempt in {1..50}; do
  if ! kill -0 "$BID" 2>/dev/null; then
    wait "$BID"
    exit $?
  fi
  if is_budget_running; then
    open "$url"
    printf 'Сервер работает. Для остановки нажми Ctrl+C в этом окне.\n'
    wait "$BID"
    exit $?
  fi
  sleep 0.1
done
printf 'Не удалось подтвердить запуск. Проверь настройки и журнал сервера выше.\n' >&2
kill -TERM "$BID" 2>/dev/null
wait "$BID"
exit 1

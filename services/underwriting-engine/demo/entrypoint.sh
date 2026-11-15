#!/bin/sh
set -u

/app/underwriting-demo serve-shortfalls --manifest /demo/manifest.json &
mock_pid=$!
sleep 1
if ! kill -0 "$mock_pid" 2>/dev/null; then
  wait "$mock_pid"
  exit $?
fi

/app/underwriting-api &
api_pid=$!
stop_children() {
  kill -TERM "$api_pid" "$mock_pid" 2>/dev/null || true
}
trap stop_children INT TERM

while kill -0 "$api_pid" 2>/dev/null && kill -0 "$mock_pid" 2>/dev/null; do
  sleep 1
done

kill -TERM "$api_pid" "$mock_pid" 2>/dev/null || true
wait "$api_pid"
api_status=$?
wait "$mock_pid"
mock_status=$?
if [ "$api_status" -ne 0 ]; then
  exit "$api_status"
fi
exit "$mock_status"

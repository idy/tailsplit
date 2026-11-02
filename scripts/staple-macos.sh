#!/bin/sh
set -eu

operation=staple
if [ "$#" -eq 2 ] && [ "$1" = --validate ]; then
  operation=validate
  shift
fi
if [ "$#" -ne 1 ]; then
  echo "Usage: staple-macos.sh [--validate] <notarized app, package or disk image>" >&2
  exit 64
fi

attempt=1
delay=2
while :; do
  status=0
  xcrun stapler "$operation" "$1" || status=$?
  if [ "$status" -eq 0 ]; then
    exit 0
  fi
  # EX_NOHOST is returned when Apple's CloudKit ticket endpoint loses its
  # connection. EX_TEMPFAIL is another explicitly temporary delivery failure.
  # Invalid artifacts and other permanent failures must still stop immediately.
  case "$status" in
    68|75) ;;
    *) exit "$status" ;;
  esac
  if [ "$attempt" -ge 5 ]; then
    exit "$status"
  fi
  echo "Apple ticket delivery failed ($status); retrying in ${delay}s (attempt $((attempt + 1))/5)." >&2
  sleep "$delay"
  attempt=$((attempt + 1))
  delay=$((delay * 2))
done

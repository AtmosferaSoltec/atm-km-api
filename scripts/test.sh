#!/usr/bin/env sh
# Runs every test, the integration ones included, against km_test (TEST_DATABASE_URL in .env).
set -eu
cd "$(dirname "$0")/.."
set -a
. ./.env
set +a
exec go test -count=1 "$@" ./...

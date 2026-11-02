#!/usr/bin/env sh
# Runs the API (or any subcommand) with the variables in .env:
#
#   scripts/dev.sh                    start the server
#   scripts/dev.sh migrate up
#   scripts/dev.sh user create --username joel --name "Joel"
set -eu
cd "$(dirname "$0")/.."
set -a
. ./.env
set +a
exec go run ./cmd/api "$@"

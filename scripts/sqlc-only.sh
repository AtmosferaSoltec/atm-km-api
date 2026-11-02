#!/usr/bin/env sh
# Generates sqlc code for the given entries only (1-based, in db/sqlc.yaml order).
# Handy while a module's queries.sql does not exist yet.
set -eu
cd "$(dirname "$0")/.."
keep="$*"
awk -v keep=" $keep " '/^  - engine/{n++} n==0 || index(keep, " " n " ")' db/sqlc.yaml > db/.sqlc-partial.yaml
trap 'rm -f db/.sqlc-partial.yaml' EXIT
"${SQLC:-$HOME/go/bin/sqlc}" generate -f db/.sqlc-partial.yaml

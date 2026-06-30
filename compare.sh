#!/usr/bin/env bash
#
# compare.sh — run the Playwright (main) and Go exporters side by side and diff
# their metrics over several iterations. Must run on a Unifique connection, since
# the LibreSpeed backend only answers from inside Unifique's network.
#
# Usage:
#   ./compare.sh [iterations]   # default: 3
#
# It builds both images (the Playwright one from a `main` worktree), brings them
# up via compare.compose.yml, then repeatedly scrapes both /metrics endpoints
# and prints a side-by-side table. Tears everything down on exit.

set -euo pipefail

ITERATIONS="${1:-3}"
COMPOSE="docker compose -f compare.compose.yml"
WORKTREE=".compare/main"
PW_URL="http://localhost:3001/metrics"
GO_URL="http://localhost:3002/metrics"

cleanup() {
  echo
  echo "==> Tearing down"
  $COMPOSE down --remove-orphans >/dev/null 2>&1 || true
  git worktree remove --force "$WORKTREE" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> Preparing 'main' worktree at $WORKTREE"
git worktree remove --force "$WORKTREE" >/dev/null 2>&1 || true
mkdir -p .compare
git worktree add --force "$WORKTREE" main >/dev/null

echo "==> Building both images (this may take a while the first time)"
$COMPOSE build

echo "==> Starting both exporters"
$COMPOSE up -d

# metric_value <url> <metric_name> — prints the gauge value, or "-" if absent.
metric_value() {
  curl -s --max-time 180 "$1" 2>/dev/null \
    | awk -v m="$2" '$1 == m { print $2; found=1 } END { if (!found) print "-" }'
}

# to_mbps <bits_per_second> — bps -> Mbps for readability; passes through "-".
to_mbps() {
  [ "$1" = "-" ] && { echo "-"; return; }
  awk -v v="$1" 'BEGIN { printf "%.2f", v / 1000000 }'
}

printf '\n%-4s | %-22s | %-22s | %-14s | %-14s\n' "run" "download Mbps (pw/go)" "upload Mbps (pw/go)" "ping (pw/go)" "jitter (pw/go)"
printf -- '-----+------------------------+------------------------+----------------+----------------\n'

for i in $(seq 1 "$ITERATIONS"); do
  pw_dl=$(to_mbps "$(metric_value "$PW_URL" speed_download_bits_per_second)")
  go_dl=$(to_mbps "$(metric_value "$GO_URL" speed_download_bits_per_second)")
  pw_ul=$(to_mbps "$(metric_value "$PW_URL" speed_upload_bits_per_second)")
  go_ul=$(to_mbps "$(metric_value "$GO_URL" speed_upload_bits_per_second)")
  pw_pi=$(metric_value "$PW_URL" speed_ping_ms)
  go_pi=$(metric_value "$GO_URL" speed_ping_ms)
  pw_ji=$(metric_value "$PW_URL" speed_jitter_ms)
  go_ji=$(metric_value "$GO_URL" speed_jitter_ms)

  printf '%-4s | %10s / %-9s | %10s / %-9s | %6s / %-5s | %6s / %-5s\n' \
    "$i" "$pw_dl" "$go_dl" "$pw_ul" "$go_ul" "$pw_pi" "$go_pi" "$pw_ji" "$go_ji"
done

echo
echo "==> Done. Values should be in the same ballpark; speed tests vary 10-30% run to run."

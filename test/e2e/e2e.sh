#!/usr/bin/env bash
# End-to-end test of a real Kayly on a real Docker host, as CI runs it (ARCHITECTURE.md §12, v0.5):
#
#   1. install.sh installs the previous release (or this build) with local certificates;
#   2. testdata/hello deploys, after a deploy that fails for a missing value;
#   3. the daemon is killed (SIGKILL) as each pipeline step starts; every time the app keeps
#      answering, the reconciler leaves only the live release's containers, and a later deploy
#      works;
#   4. a rollback, then an upgrade to this build, while a client requests the app every 50 ms:
#      not one request may fail.
#
# Usage, as root on a disposable host: test/e2e/e2e.sh <kayly built from this tree> [<previous release>]
set -euo pipefail

new=$(realpath "$1")
old=$(realpath "${2:-$1}")
repo=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
app=https://web.hello.localhost
failures=$work/failures

say() { printf '\n\033[1m=== %s\033[0m\n' "$*"; }
die() {
  printf '\033[31mFAIL: %s\033[0m\n' "$*" >&2
  journalctl -u kayly --no-pager -n 80 >&2 || true
  exit 1
}
kayly() { /usr/local/bin/kayly "$@"; }

# until SECONDS COMMAND...: retry COMMAND every second until it succeeds or time runs out.
until_ok() {
  local deadline=$((SECONDS + $1))
  shift
  until "$@" >/dev/null 2>&1; do
    [ $SECONDS -lt $deadline ] || return 1
    sleep 1
  done
}

daemon_up() { kayly projects; }
live() { kayly releases -p hello | awk '$2 == "live" { print substr($1, 2) }'; }
greeting() { curl -sfk --max-time 5 "$app/" | sed -n 's/.*"greeting":"\([^"]*\)".*/\1/p'; }

# A client that never stops asking: every failed request is written down.
load() {
  : >"$failures"
  while :; do
    code=$(curl -sk -o /dev/null --max-time 5 -w '%{http_code}' "$app/" || true)
    [ "$code" = 200 ] || echo "$(date +%T.%N) $code" >>"$failures"
    sleep 0.05
  done
}
assert_no_failed_requests() {
  if [ -s "$failures" ]; then
    die "$(wc -l <"$failures") requests failed during $1: $(head -n 5 "$failures" | tr '\n' ' ')"
  fi
}

# Containers left from releases that are not live are garbage the reconciler must collect.
only_live_containers() {
  local want got
  want=$(live)
  got=$(docker ps -a --filter label=dev.kayly.project=hello --format '{{.Label "dev.kayly.release"}}' | sort -u | tr '\n' ' ')
  [ "$got" = "$want " ]
}
no_release_in_progress() { ! kayly releases -p hello | awk 'NR > 1 { print $2 }' | grep -Eq 'building|releasing|starting'; }

say "install the previous release ($old)"
KAYLY_BINARY=$old KAYLY_BASE_DOMAIN=localhost sh "$repo/install.sh"
until_ok 60 daemon_up || die "the daemon did not answer after install"

say "deploy testdata/hello"
cp -r "$repo/testdata/hello" "$work/hello"
cd "$work/hello"
if out=$(kayly deploy 2>&1); then die "a deploy with GREETING unset went live"; fi
grep -q GREETING <<<"$out" || die "the failed deploy did not name the missing value: $out"
kayly env set GREETING=one
kayly deploy || die "first deploy"
until_ok 60 test "$(greeting)" = one || die "the app does not answer with its greeting"

load &
loader=$!
trap 'kill $loader 2>/dev/null || true; rm -rf "$work"' EXIT

for step in build 'release:' start health switch drain; do
  say "kill the daemon as '$step' starts"
  before=$(live)
  kayly deploy >"$work/deploy.log" 2>&1 &
  client=$!
  until_ok 600 grep -q "^==> $step" "$work/deploy.log" || die "the deploy never reached '$step': $(tail -n 20 "$work/deploy.log")"
  systemctl kill --signal=SIGKILL kayly
  wait "$client" || true # the stream ends with the daemon
  until_ok 120 daemon_up || die "the daemon did not come back after a crash at '$step'"
  until_ok 120 no_release_in_progress || die "a release stayed in progress after a crash at '$step'"
  until_ok 120 only_live_containers || die "containers of a dead release survived a crash at '$step': $(docker ps -a --filter label=dev.kayly.project=hello --format '{{.Names}}')"
  now=$(live)
  case $step in
    switch | drain) [ "$now" -ge "$before" ] || die "the live release went back from r$before to r$now" ;;
    *) [ "$now" = "$before" ] || die "a crash before the switch changed the live release from r$before to r$now" ;;
  esac
  assert_no_failed_requests "a crash at '$step'"
done

say "a deploy after all that"
kayly env set GREETING=two
kayly deploy || die "deploy after the crashes"
until_ok 30 test "$(greeting)" = two || die "the new release does not answer"
current=$(live)

say "roll back"
previous=$(kayly releases -p hello | awk -v cur="$current" '$2 == "superseded" && substr($1, 2) < cur { print substr($1, 2); exit }')
[ -n "$previous" ] || die "no release to roll back to"
kayly rollback "r$previous" || die "rollback"
[ "$(live)" = "$previous" ] || die "r$previous is not live after the rollback"
assert_no_failed_requests "deploys and the rollback"

say "upgrade to this build under load"
"$new" upgrade -binary "$new" || die "upgrade"
cmp -s "$new" /usr/local/bin/kayly || die "the daemon's binary was not replaced"
[ -f /usr/local/bin/kayly.previous ] || die "the previous binary was not kept"
[ "$(live)" = "$previous" ] || die "the upgrade changed the live release"
sleep 2
assert_no_failed_requests "the upgrade"

kill "$loader"
say "passed: crash at every step, rollback and upgrade with zero failed requests"

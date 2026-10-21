#!/usr/bin/env bash
# Prove `dws refresh` leaves a run record (sim/design/ops-dashboard.md section 4.1 and 4.2 item 1, test 10):
#   - a run that changes nothing still appends exactly one refresh-run event, and nothing else;
#   - the event is the run's last append, by actor refresh, tag mirror, with rows, links, warnings and at;
#   - refresh --local writes one too, with "local": true;
#   - the fold ignores it: rows and the plain `dws show` table are the same with it as without it;
#   - `dws show --json` reports the newest one as last_refresh, and null when there is none.
# Written for bash 3.2 and bash 5. Scratch DIRECTOR_STATE and HOME only.
#
# Usage: skills/director/scripts/verify-dws-refresh-run.sh   Exit nonzero on any miss.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export HOME="$root/home"; mkdir -p "$HOME" "$root/bin"
export DIRECTOR_STATE="$root/state"; mkdir -p "$DIRECTOR_STATE"
export DWS_TEST_CLOCK=1 DWS_NOW=2030-12-31T00:00:00Z
unset DWS_NO_RERENDER
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
export RELAY_LOG="$root/relay.md"; : >"$RELAY_LOG"
export CALLS="$root/calls"; : >"$CALLS"
# gh and bd must not be reached in these runs; a call is logged and fails
for t in gh bd; do printf '#!/bin/sh\necho "%s $*" >>"$CALLS"\nexit 1\n' "$t" >"$root/bin/$t"; chmod +x "$root/bin/$t"; done
export PATH="$root/bin:$PATH"

lines() { wc -l <"$LEDGER" | tr -d ' '; }
DWS_NOW=2030-09-01T00:00:00Z "$DWS" open alpha --title "a row with no link" --stage idea >/dev/null 2>&1
DWS_NOW=2030-09-01T00:00:00Z "$DWS" open beta --title "a row read recently" --stage building --link pr:o/r#4 >/dev/null 2>&1
printf '{"ts":"2030-12-30T23:59:00Z","actor":"refresh","tag":"mirror","event":"observe","ws":"beta","link":"pr:o/r#4","seen":{"kind":"pr","state":"draft"},"checked_at":"2030-12-30T23:59:00Z"}\n' >>"$LEDGER"
before="$(lines)"
cp "$LEDGER" "$root/ledger.before"
"$DWS" show >"$root/show.before" 2>&1
export DWS_REFRESH_SKIP_SECONDS=3600

"$DWS" refresh >"$root/o" 2>"$root/e"; rc=$?
[[ $rc -eq 0 ]] && ok "refresh runs on a ledger read a minute ago" || bad "refresh runs" "rc=$rc $(cat "$root/e")"
[[ "$(( $(lines) - before ))" == 1 ]] && ok "it appends exactly one line" || bad "exactly one line" "$(( $(lines) - before ))"
[[ ! -s "$CALLS" ]] && ok "and fetched nothing (the link was inside the skip window)" || bad "no fetch" "$(cat "$CALLS")"
tail -n 1 "$LEDGER" >"$root/last"
python3 - "$root/last" <<'PY' && ok "the line is a refresh-run by refresh, tag mirror, with rows, links, warnings and at" || bad "refresh-run shape" "$(cat "$root/last")"
import json, sys
e = json.load(open(sys.argv[1]))
assert e["event"] == "refresh-run" and e["actor"] == "refresh" and e["tag"] == "mirror", e
assert e["at"] == "2030-12-31T00:00:00Z" and e["rows"] == 2 and e["links"] == 0 and e["warnings"] == 0, e
assert "local" not in e and "ws" not in e, e
PY
head -n "$before" "$LEDGER" | cmp -s - "$root/ledger.before" && ok "nothing earlier in the ledger changed" || bad "earlier lines unchanged"
"$DWS" show >"$root/show.after" 2>&1
cmp -s "$root/show.before" "$root/show.after" && ok "the fold ignores it: the plain table is the same" || bad "fold ignores it"
"$DWS" show --json | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["last_refresh"]["at"]=="2030-12-31T00:00:00Z", d.get("last_refresh"); assert [r["slug"] for r in d["rows"]]==["beta","alpha"] or sorted(r["slug"] for r in d["rows"])==["alpha","beta"]' \
  && ok "show --json reports the newest as last_refresh and the same two rows" || bad "last_refresh in show --json"

DWS_NOW=2030-12-31T01:00:00Z "$DWS" refresh --local >"$root/o" 2>"$root/e"
tail -n 1 "$LEDGER" | python3 -c 'import json,sys; e=json.load(sys.stdin); assert e["event"]=="refresh-run" and e["local"] is True and e["at"]=="2030-12-31T01:00:00Z", e' \
  && ok "refresh --local writes one with local true, as its last append" || bad "local run record" "$(tail -n 1 "$LEDGER") $(cat "$root/e")"
"$DWS" show --json | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["last_refresh"]["at"]=="2030-12-31T01:00:00Z" and d["last_refresh"]["local"] is True' \
  && ok "the newest of either kind is last_refresh" || bad "newest of either kind"

# a fill the board renderer launches (DWS_NO_RERENDER=1) is not a run by director and leaves no record,
# so the page's "ledger Nm" age still shows when the ledger was last really written
n0="$(lines)"
DWS_NO_RERENDER=1 "$DWS" refresh --local >/dev/null 2>&1
[[ "$(lines)" == "$n0" ]] && ok "a render-launched fill appends no run record" || bad "render-launched fill appends nothing" "$(( $(lines) - n0 ))"
DWS_NO_RERENDER=1 "$DWS" refresh >/dev/null 2>&1
[[ "$(lines)" == "$n0" ]] && ok "and neither does a render-launched refresh" || bad "render-launched refresh appends nothing"

rm -f "$LEDGER"; DWS_NOW=2030-09-01T00:00:00Z "$DWS" open gamma --title "g" --stage idea >/dev/null 2>&1
"$DWS" show --json | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["last_refresh"] is None, d["last_refresh"]' \
  && ok "with no run on record last_refresh is null, not a date" || bad "last_refresh null"
"$DWS" refresh nosuch >/dev/null 2>&1 && bad "a refused run writes no record" || { [[ "$(lines)" == 1 ]] && ok "a refused run (unknown slug) appends no run record" || bad "a refused run writes no record"; }

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]

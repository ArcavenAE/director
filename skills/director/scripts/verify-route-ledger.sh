#!/usr/bin/env bash
# Prove `route-ledger check` against sim/design/route-ledger.md: it reads one route-ledger file (JSON lines) and
# checks each row against route-record@1 or route-event@1, accepting the legacy shapes the ledgers hold today
# and counting them, so queue depth and closure can be read from any supervisor's ledger the same way.
#   - the example fixture reads as its documented counts (rows, asks, routed, acked, closed, cancelled, open,
#     depth known, event@1, legacy);
#   - every refusal names the row and one fixed field:reason word, never a value, and the run exits 1;
#   - it reads only the file it is given and writes nothing; a missing file or usage error is exit 2.
# Fixture values are placeholders. Every run is under `env -i` with a scratch HOME. Bash 3.2 and 5.
#
# Usage: skills/director/scripts/verify-route-ledger.sh   Exit nonzero on any miss.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
L="${ROUTE_LEDGER:-$here/route-ledger}"
fx="$here/fixtures/route-ledger.example.jsonl"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
[[ -f "$L" ]] || { echo "FAIL route-ledger exists at $L"; echo "0 passed, 1 failed"; exit 1; }

root="$(mktemp -d "${LEDGER_TMP:-${TMPDIR:-/tmp}}/rl.XXXXXX")" || { echo "FAIL scratch root"; exit 1; }
root="$(cd "$root" && pwd -P)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/home"
PYDIR="$(dirname "$(command -v python3)")"

run() {
  env -i HOME="$root/home" PATH="$PYDIR:/usr/bin:/bin" python3 "$L" "$@" >"$root/out" 2>"$root/err"
  rc=$?
}
refused() { # refused <name> <row N: field:reason>
  if [[ $rc -eq 1 ]] && grep -qF "route-ledger: refused: $2" "$root/err"; then ok "$1"
  else bad "$1" "rc=$rc err=$(head -c 200 "$root/err" | tr '\n' ' ')"; fi
}
row() { printf '%s\n' "$1" >"$root/l.jsonl"; run check "$root/l.jsonl"; }
REC='"ask_id":"ph-1","routed_to":"role://t/builder","basis":"b","principal":"p","supervisor_epoch":"g1-0","routed_at":"2030-01-01T10:00:00Z"'

# --- the example fixture ----------------------------------------------------------------------------
run check "$fx"
[[ $rc -eq 0 ]] && ok "the example ledger passes (exit 0)" || bad "the example ledger passes" "rc=$rc $(cat "$root/err")"
want='route-ledger: rows 10, asks 4, routed 4, acked 3, closed 2, cancelled 1, open 1, depth known 2 of 4, event@1 2, legacy 4, refused 0'
[[ "$(cat "$root/out")" == "$want" ]] && ok "it reads as the documented counts" || bad "the counts" "got: $(cat "$root/out")"
run check "$fx" --json
python3 - "$root/out" <<'PY' && ok "--json prints the same counts as one object" || bad "--json counts" "$(head -c 300 "$root/out")"
import json, sys
d = json.load(open(sys.argv[1]))
assert d == {"rows": 10, "asks": 4, "routed": 4, "acked": 3, "closed": 2, "cancelled": 1, "open": 1,
             "depth_known": 2, "depth_total": 4, "event_v1": 2, "legacy": 4, "refused": 0}, d
PY

# --- records ------------------------------------------------------------------------------------------
row "{$REC}"; [[ $rc -eq 0 ]] && ok "a record with only the required fields passes (depth unknown)" || bad "minimal record" "$(cat "$root/err")"
grep -qF 'depth known 0 of 1' "$root/out" && ok "a record with no depth counts as depth unknown" || bad "unknown depth count" "$(cat "$root/out")"
for f in ask_id routed_to basis principal supervisor_epoch routed_at; do
  python3 - "$f" >"$root/l.jsonl" <<'PY'
import json, sys
r = {"ask_id": "ph-1", "routed_to": "role://t/builder", "basis": "b", "principal": "p", "supervisor_epoch": "g1-0", "routed_at": "2030-01-01T10:00:00Z"}
del r[sys.argv[1]]
print(json.dumps(r))
PY
  if [[ "$f" == routed_to ]]; then
    # without routed_to the row is not a record at all; it must still be refused, as unrecognized
    run check "$root/l.jsonl"; refused "a record missing routed_to is refused as unrecognized" "row 1: row:unrecognized"
  else run check "$root/l.jsonl"; refused "a record missing $f is refused" "row 1: $f:missing"; fi
done
row "{${REC/2030-01-01T10:00:00Z/yesterday}}"; refused "an unreadable routed_at is refused" "row 1: routed_at:time"
row "{$REC,\"depth_at_route\":-1}"; refused "a negative depth is refused" "row 1: depth_at_route:number"
row "{$REC,\"depth_at_route\":1.5}"; refused "a fractional depth is refused" "row 1: depth_at_route:number"
row "{$REC,\"depth_at_route\":\"2\"}"; refused "a string depth is refused" "row 1: depth_at_route:number"
row "{$REC,\"depth_at_route\":true}"; refused "a boolean depth is refused" "row 1: depth_at_route:number"
row "{$REC,\"depth_at_route\":null}"; grep -qF 'depth known 0 of 1' "$root/out" && ok "a null depth_at_route counts as depth unknown" || bad "null depth_at_route" "$(cat "$root/out")"
row "{$REC,\"depth_observed\":null}"; [[ $rc -eq 0 ]] && ok "a null depth_observed passes (not observed)" || bad "null depth_observed" "$(cat "$root/err")"
row "{$REC,\"acked_at\":\"soon\"}"; refused "an unreadable acked_at is refused" "row 1: acked_at:time"
row "{$REC,\"closed_at\":\"2030-01-01T12:00:00Z\"}"; [[ $rc -eq 0 ]] && grep -qF 'closed 1' "$root/out" && grep -qF 'open 0' "$root/out" && ok "a record with closed_at counts the ask as closed, not open" || bad "record closed_at" "rc=$rc $(cat "$root/out")"
row "{$REC,\"closed_at\":\"later\"}"; refused "an unreadable closed_at on a record is refused" "row 1: closed_at:time"
row "{$REC,\"cancelled_at\":\"2030-01-01T10:01:00Z\"}"; grep -qF 'cancelled 1' "$root/out" && ok "cancelled_at counts the ask as cancelled" || bad "cancelled count" "$(cat "$root/out")"
row "{$REC,\"alternatives\":\"one\"}"; refused "alternatives must be a list" "row 1: alternatives:shape"
row "{$REC,\"no_route\":5}"; refused "no_route must be an object or text" "row 1: no_route:shape"
row "{$REC,\"extra_field\":\"kept\"}"; [[ $rc -eq 0 ]] && ok "an unknown field on a record is allowed (ledgers carry local extras)" || bad "unknown field" "$(cat "$root/err")"

# --- events -------------------------------------------------------------------------------------------
EV='"ask_id":"ph-1","supervisor_epoch":"g1-0","at":"2030-01-01T10:05:00Z"'
row "{$EV,\"kind\":\"ack\",\"acked_at\":\"2030-01-01T10:05:00Z\",\"text\":\"t\"}"; [[ $rc -eq 0 ]] && ok "an ack event passes" || bad "ack event" "$(cat "$root/err")"
row "{$EV,\"kind\":\"ack\",\"text\":\"t\"}"; refused "an ack event with no acked_at is refused" "row 1: acked_at:missing"
row "{$EV,\"kind\":\"close\",\"closed_at\":\"2030-01-01T10:05:00Z\",\"result\":\"r\"}"; [[ $rc -eq 0 ]] && ok "a close event passes" || bad "close event" "$(cat "$root/err")"
row "{$EV,\"kind\":\"close\",\"closed_at\":\"2030-01-01T10:05:00Z\"}"; refused "a close event with no result is refused" "row 1: result:missing"
row "{$EV,\"kind\":\"close\",\"result\":\"r\"}"; refused "a close event with no closed_at is refused" "row 1: closed_at:missing"
for k in relay note cancel; do row "{$EV,\"kind\":\"$k\",\"text\":\"t\"}"; [[ $rc -eq 0 ]] && ok "a $k event passes" || bad "$k event" "$(cat "$root/err")"; done
row "{$EV,\"kind\":\"shrug\",\"text\":\"t\"}"; refused "an unknown event kind is refused" "row 1: kind:unknown"
row "{\"ask_id\":\"ph-1\",\"supervisor_epoch\":\"g1-0\",\"at\":\"soon\",\"kind\":\"note\",\"text\":\"t\"}"; refused "an unreadable event time is refused" "row 1: at:time"
row "{$EV,\"kind\":\"note\"}"; refused "an event with no text is refused" "row 1: text:missing"
row "{\"ask_id\":\"\",\"supervisor_epoch\":\"g1-0\",\"at\":\"2030-01-01T10:05:00Z\",\"kind\":\"note\",\"text\":\"t\"}"; refused "an empty ask_id is refused" "row 1: ask_id:missing"

# --- legacy rows are accepted and counted ---------------------------------------------------------------
row '{"ask_id":"ph-1","event":"legacy text","supervisor_epoch":"g1-0"}'; grep -qF 'legacy 1' "$root/out" && ok "a legacy event with free text counts as legacy" || bad "legacy event" "$(cat "$root/out")"
row '{"ask_id":"ph-1","acked_at":"2030-01-01T10:05:00Z","event":"x","supervisor_epoch":"g1-0"}'; grep -qF 'acked 1' "$root/out" && ok "a legacy event with acked_at counts the ask as acked" || bad "legacy ack" "$(cat "$root/out")"
row '{"ask_id":"ph-1","result":"r"}'; grep -qF 'closed 1' "$root/out" && ok "a bare result row counts the ask as closed" || bad "bare close" "$(cat "$root/out")"
row '{"ask_id":"ph-1","closed_at":"2030-01-01T10:05:00Z","result":"r"}'; grep -qF 'closed 1' "$root/out" && ok "a result row with closed_at counts as closed" || bad "close with time" "$(cat "$root/out")"
row '{"at":"2030-01-01T10:05:00Z","note":"free note with no ask"}'; [[ $rc -eq 0 ]] && ok "a legacy note with no ask_id passes" || bad "legacy note" "$(cat "$root/err")"
row '{"ask_id":"ph-1","closed_at":"later","result":"r"}'; refused "a legacy close with an unreadable time is refused" "row 1: closed_at:time"

# --- file-level refusals ------------------------------------------------------------------------------
printf '%s\n' "{$REC}" "not json" "{$REC}" >"$root/l.jsonl"; run check "$root/l.jsonl"
refused "a line that is not JSON is refused, naming its row" "row 2: row:json"
grep -qF 'rows 3' "$root/out" && grep -qF 'refused 1' "$root/out" && ok "a refusal does not stop the count: the other rows are still read" || bad "count continues" "$(cat "$root/out")"
printf '%s\n' '[1,2]' >"$root/l.jsonl"; run check "$root/l.jsonl"; refused "a row that is not an object is refused" "row 1: row:shape"
printf '%s\n' '{"hello":"world"}' >"$root/l.jsonl"; run check "$root/l.jsonl"; refused "an object with no known shape is refused" "row 1: row:unrecognized"
printf '\n\n%s\n' "{$REC}" >"$root/l.jsonl"; run check "$root/l.jsonl"; [[ $rc -eq 0 ]] && grep -qF 'rows 1' "$root/out" && ok "blank lines are skipped, not counted" || bad "blank lines" "$(cat "$root/err")"
python3 -c "print('{\"ask_id\":\"' + 'x'*70000 + '\"}')" >"$root/l.jsonl"; run check "$root/l.jsonl"; refused "a line over 64 KiB is refused" "row 1: row:length"
: >"$root/l.jsonl"; run check "$root/l.jsonl"; [[ $rc -eq 0 ]] && grep -qF 'rows 0' "$root/out" && ok "an empty ledger reads as zero rows" || bad "empty ledger" "$(cat "$root/err")"
printf '%s\n' "{\"ask_id\":\"canary-ask\",\"routed_to\":\"canary-to\",\"basis\":\"canary-basis\",\"principal\":\"p\",\"supervisor_epoch\":\"g1-0\",\"routed_at\":\"canary-time\"}" >"$root/l.jsonl"; run check "$root/l.jsonl"
! grep -q canary "$root/err" "$root/out" && ok "a refusal never echoes a value" || bad "a refusal echoes a value" "$(cat "$root/err")"

# --- usage, and it writes nothing -----------------------------------------------------------------------
run check "$root/missing.jsonl"; [[ $rc -eq 2 ]] && ok "a missing file is exit 2" || bad "missing file" "rc=$rc"
run; [[ $rc -eq 2 ]] && ok "no subcommand is exit 2" || bad "no subcommand" "rc=$rc"
run bogus; [[ $rc -eq 2 ]] && ok "an unknown subcommand is exit 2" || bad "unknown subcommand" "rc=$rc"
run check; [[ $rc -eq 2 ]] && ok "check with no file is exit 2" || bad "check with no file" "rc=$rc"
cp "$fx" "$root/keep.jsonl"; before="$(cd "$root" && find . -type f ! -name out ! -name err | LC_ALL=C sort | xargs shasum | shasum | cut -c1-16)"
run check "$root/keep.jsonl"; run check "$root/keep.jsonl" --json
after="$(cd "$root" && find . -type f ! -name out ! -name err | LC_ALL=C sort | xargs shasum | shasum | cut -c1-16)"
[[ "$before" == "$after" ]] && ok "no file under the scratch tree changed" || bad "a file changed"
cmp -s "$fx" "$root/keep.jsonl" && ok "the ledger file is byte for byte as it was" || bad "the ledger changed"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]

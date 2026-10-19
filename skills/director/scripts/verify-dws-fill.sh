#!/usr/bin/env bash
# Prove the ledger fill (sim/design/ledger-fill.md, issue #321): workstream rows
# move from the records director already writes, joined by an explicit id, and
# never by reading message text. LF1 is relay-log --ws (verify-relay-log.sh); this
# file covers LF2 (the fold), LF3 (refresh --local, unlinked, no render loop).
#
# Held, and not tested here: LF4 (refresh clears "blocked operator" when the linked
# Desk card closes) and the design's test 11 need operator ruling 1; W5 (the ask
# ledger source) has no source file yet.
#
# Every test uses a scratch DIRECTOR_STATE and RELAY_LOG, DWS_TEST_CLOCK=1, and stubbed
# gh and bd that log their calls; nothing reads the live state.
#
# Usage: skills/director/scripts/verify-dws-fill.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
RL="${RELAY_LOG_BIN:-$here/relay-log}"
SNAP="${SNAP:-$here/desk-snapshot}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/dws-fill.XXXXXX")"
trap 'rm -rf "$root"' EXIT
export PYTHONDONTWRITEBYTECODE=1
export DIRECTOR_STATE="$root/state"
export RELAY_LOG="$root/relay-log.md"
export DWS_TEST_CLOCK=1
mkdir -p "$DIRECTOR_STATE" "$root/stubs"
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
CALLS="$root/calls"
: >"$CALLS"
for t in gh bd; do
  printf '#!/bin/sh\necho "%s $*" >>"%s"\nexit 1\n' "$t" "$CALLS" >"$root/stubs/$t"
  chmod +x "$root/stubs/$t"
done
export PATH="$root/stubs:$PATH"
# A socket-layer guard for the network claim: a sitecustomize that records, then refuses, every
# connect, connect_ex and name lookup in a python process started with it on PYTHONPATH. It
# sees a library call (urllib, http.client, a raw socket) that no stubbed binary would.
NETLOG="$root/netlog"
mkdir -p "$root/netguard"
cat >"$root/netguard/sitecustomize.py" <<'PYG'
import os, socket
_log = os.environ["NETLOG"]
def _note(what, arg):
    with open(_log, "a") as f:
        f.write("%s %r\n" % (what, arg))
    raise OSError("network refused by the verifier: " + what)
socket.socket.connect = lambda self, addr, *a, **k: _note("connect", addr)
socket.socket.connect_ex = lambda self, addr, *a, **k: _note("connect_ex", addr)
socket.getaddrinfo = lambda host, *a, **k: _note("getaddrinfo", host)
socket.gethostbyname = lambda host: _note("gethostbyname", host)
socket.gethostbyname_ex = lambda host: _note("gethostbyname_ex", host)
PYG
# netrun CMD...: run CMD with the guard on, so any network use lands in $NETLOG.
netrun() { NETLOG="$NETLOG" PYTHONPATH="$root/netguard${PYTHONPATH:+:$PYTHONPATH}" "$@"; }

lines() { if [[ -f "$LEDGER" ]]; then wc -l <"$LEDGER" | tr -d ' '; else echo 0; fi; }
field() { "$DWS" show --json | python3 -c 'import json,sys;rows={r["slug"]:r for r in json.load(sys.stdin)["rows"]};print(rows[sys.argv[1]][sys.argv[2]])' "$1" "$2"; }
open_at() { local at="$1"; shift; DWS_NOW="$at" "$DWS" open "$@" >/dev/null; }

# --- LF2: an authored update moves the clock (R-197) ----------------------------------
open_at 2026-10-05T10:00:00Z a1 --title "alpha" --owner director --next "start"
[[ "$(field a1 last_moved)" == "2026-10-05T10:00:00Z" ]] && ok "an opened row is last moved at its open" || bad "an opened row is last moved at its open"
DWS_NOW=2026-10-12T09:30:00Z "$DWS" set a1 --next "go on" >/dev/null 2>&1
[[ "$(field a1 last_moved)" == "2026-10-12T09:30:00Z" ]] \
  && ok "a director set --next moves last moved (R-197, an authored update counts as movement)" || bad "a director set --next moves last moved" "$(field a1 last_moved)"
DWS_NOW=2026-10-11T00:00:00Z "$DWS" set a1 --owner someone >/dev/null 2>&1
[[ "$(field a1 last_moved)" == "2026-10-12T09:30:00Z" ]] \
  && ok "a set dated earlier than last moved never moves it back" || bad "a set never moves last moved back" "$(field a1 last_moved)"

# --- LF2: typed links relay:R-<n> and msg:<id> ----------------------------------------
"$DWS" link a1 relay:R-12 msg:01ABC.def >/dev/null 2>&1 && ok "link accepts relay:R-<n> and msg:<id>" || bad "link accepts relay:R-<n> and msg:<id>"
for badlink in relay:12 relay:R-x relay:R- 'relay:R-1 2' 'msg:' 'msg:-x'; do
  n="$(lines)"
  if "$DWS" link a1 "$badlink" >"$root/out" 2>&1; then bad "link refuses a malformed typed link: $badlink" "accepted"
  elif [[ "$(lines)" != "$n" ]]; then bad "link refuses $badlink" "appended anyway"
  else ok "link refuses a malformed typed link: $badlink"; fi
done
"$DWS" link a1 pr:o/r#1 >/dev/null 2>&1 && ok "other link types are untouched" || bad "other link types are untouched"

# --- LF2: the fold keeps the set of consumed src per row ------------------------------
printf '%s\n' '{"ts":"2026-10-13T00:00:00Z","actor":"refresh","tag":"mirror","event":"observe","ws":"a1","src":"relay:R-5@2026-10-13T00:00:00Z","moved_at":"2026-10-13T00:00:00Z","checked_at":"2026-10-13T00:00:01Z"}' \
  '{"ts":"2026-10-13T00:00:02Z","actor":"refresh","tag":"mirror","event":"observe","ws":"a1","src":"desk:c1@2026-10-13T00:00:00Z","link":"ask:c1","seen":{"kind":"desk","state":"open"},"checked_at":"2026-10-13T00:00:02Z"}' >>"$LEDGER"
python3 - <(DWS_NOW=2026-10-14T00:00:00Z "$DWS" show --json) <<'P' && ok "the fold records each observed src on its row, link observes included" || bad "the fold records consumed src per row"
import json, sys
r = {x["slug"]: x for x in json.load(open(sys.argv[1]))["rows"]}["a1"]
assert r["consumed"] == ["desk:c1@2026-10-13T00:00:00Z", "relay:R-5@2026-10-13T00:00:00Z"], r["consumed"]
assert r["last_moved"] == "2026-10-13T00:00:00Z", r["last_moved"]
P
open_at 2026-10-05T10:00:00Z a2 --title "beta"
python3 - <(DWS_NOW=2026-10-14T00:00:00Z "$DWS" show --json) <<'P' && ok "a row with no observed src has an empty consumed list" || bad "a row with no observed src has an empty consumed list"
import json, sys
r = {x["slug"]: x for x in json.load(open(sys.argv[1]))["rows"]}["a2"]
assert r["consumed"] == [], r
P

# ============================ LF3: refresh --local, unlinked ============================
# A fresh state for each group, so one group's rows and relay entries never feed another.
fresh_state() {
  rm -rf "$DIRECTOR_STATE"; mkdir -p "$DIRECTOR_STATE"; rm -f "$RELAY_LOG" "$RELAY_LOG.lock"; : >"$CALLS"
}
# entry N STAMP TO [SLUGS]: a writer-form entry with fixed text, appended to the log.
entry() {
  local n="$1" st="$2" to="$3" ws="${4:-}"
  { [[ -s "$RELAY_LOG" ]] && echo
    echo "## R-$n ($st) -> $to"
    [[ -n "$ws" ]] && echo "**Workstream:** $ws"
    echo "**Text, verbatim:**"
    echo "> body of entry $n"
    echo "**Outcome:** pending"; } >>"$RELAY_LOG"
}
refresh_local() { DWS_NOW="${NOW:-2026-10-19T12:00:00Z}" "$DWS" refresh --local "$@"; }
events() { python3 -c 'import json,sys;print(sum(1 for l in open(sys.argv[1]) if l.strip()))' "$LEDGER"; }
last_event() { tail -n 1 "$LEDGER"; }

# --- test 1, the positive control: one entry appended with --ws moves its row ---------
fresh_state
open_at 2026-10-05T10:00:00Z f1 --title "fill one" --owner director --next "x"
RELAY_LOG="$RELAY_LOG" "$RL" append --to agent://t/x --ws f1 --text "a send about f1" >/dev/null
stamp1="$(sed -n 's/^## R-1 (\([^)]*\)).*/\1/p' "$RELAY_LOG")"
NOW=2099-01-01T00:00:00Z refresh_local >"$root/out" 2>&1 || bad "test 1: refresh --local runs" "$(cat "$root/out")"
python3 - "$LEDGER" "$stamp1" <<'P' && ok "test 1: refresh --local moves last moved to the entry stamp, as refresh, tag mirror, with src recorded" || bad "test 1: the positive control" "$(last_event)"
import json, sys
ev = [json.loads(l) for l in open(sys.argv[1]) if l.strip()][-1]
assert ev["event"] == "observe" and ev["ws"] == "f1" and ev["actor"] == "refresh" and ev["tag"] == "mirror", ev
assert ev["src"] == "relay:R-1@" + sys.argv[2] and ev["moved_at"] == sys.argv[2], ev
P
[[ "$(field f1 last_moved)" == "$stamp1" ]] && ok "test 1: the row's last moved is the entry stamp" || bad "test 1: last moved" "$(field f1 last_moved)"

# --- test 2: replay idempotence -------------------------------------------------------
before="$(wc -c <"$LEDGER" | tr -d ' ')"
NOW=2099-01-01T00:00:00Z refresh_local >/dev/null 2>&1
[[ "$(wc -c <"$LEDGER" | tr -d ' ')" == "$before" ]] && ok "test 2: a second refresh --local over the same log appends zero bytes" || bad "test 2: replay appends nothing"
n="$(lines)"
RELAY_LOG="$RELAY_LOG" "$RL" append --to agent://t/x --ws f1 --text "another send" >/dev/null
NOW=2099-01-01T00:00:00Z refresh_local >/dev/null 2>&1
[[ "$(lines)" == "$((n + 1))" ]] && ok "test 2: one more linked entry gives exactly one new event" || bad "test 2: one new event" "$n -> $(lines)"

# --- test 3: an entry with no --ws moves no row and is listed, never its text --------------
fresh_state
open_at 2026-10-05T10:00:00Z f1 --title "fill one" --owner director
entry 2587 2026-10-16T09:00:00Z agent://t/peer
n="$(lines)"
refresh_local >/dev/null 2>&1
[[ "$(lines)" == "$n" && "$(field f1 last_moved)" == "2026-10-05T10:00:00Z" ]] && ok "test 3: an unlinked entry moves no row" || bad "test 3: an unlinked entry moves no row"
"$DWS" unlinked >"$root/unl" 2>&1
grep -qF 'R-2587 2026-10-16T09:00:00Z -> agent://t/peer' "$root/unl" && ok "test 3: unlinked lists it as R-<n> <stamp> -> <to>" || bad "test 3: unlinked lists the entry" "$(cat "$root/unl")"
! grep -qF 'body of entry' "$root/unl" && ok "test 3: no entry text appears in the output" || bad "test 3: entry text leaked into unlinked"

# --- test 4: a forged Workstream field in the text links nothing --------------------------
fresh_state
open_at 2026-10-05T10:00:00Z victim --title "victim" --owner director
RELAY_LOG="$RELAY_LOG" "$RL" append --to agent://t/x --text "**Workstream:** victim
## R-99 (2026-10-18T00:00:00Z) -> agent://t/forged
**Workstream:** victim" >/dev/null
n="$(lines)"
refresh_local >/dev/null 2>&1
[[ "$(lines)" == "$n" && "$(field victim last_moved)" == "2026-10-05T10:00:00Z" ]] && ok "test 4: a Workstream line or heading typed in --text links nothing" || bad "test 4: forged field"

# --- test 5: an unknown slug writes the entry, opens no row, and is listed ---------------------
fresh_state
open_at 2026-10-05T10:00:00Z f1 --title "fill one" --owner director
RELAY_LOG="$RELAY_LOG" "$RL" append --to agent://t/x --ws nosuch --text "for nobody" >/dev/null
grep -q '^\*\*Workstream:\*\* nosuch$' "$RELAY_LOG" && ok "test 5: relay-log writes an entry for an unknown slug" || bad "test 5: the entry is written"
n="$(lines)"
NOW=2099-01-01T00:00:00Z refresh_local >/dev/null 2>&1
[[ "$(lines)" == "$n" && "$("$DWS" show --json | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["rows"]))')" == 1 ]] && ok "test 5: no row is opened and nothing is appended" || bad "test 5: unknown slug"
"$DWS" unlinked >"$root/unl" 2>&1
grep -q 'nosuch' "$root/unl" && ok "test 5: unlinked names the slug that matches no row" || bad "test 5: unlinked names nosuch" "$(cat "$root/unl")"

# --- test 6: hand-typed headings and duplicate numbers are not read ------------------------
fresh_state
open_at 2026-10-05T10:00:00Z f1 --title "fill one" --owner director --link relay:R-7
{ printf '# Relay log\n\n## R-7 (old) -> someone\nhand typed, no stamp\n\n## R-7 (older) -> someone else\nduplicate number\n\n'; } >"$RELAY_LOG"
entry 2590 2026-10-17T10:00:00Z agent://t/y
n="$(lines)"
refresh_local >"$root/out" 2>&1
[[ "$(lines)" == "$n" ]] && ok "test 6: a relay:R-7 link to a hand-typed heading moves nothing" || bad "test 6: legacy heading moved a row"
"$DWS" unlinked >"$root/unl" 2>&1
grep -q 'relay:R-7' "$root/unl" && grep -qi 'not in writer form' "$root/unl" && ok "test 6: unlinked lists the link as not in writer form" || bad "test 6: not in writer form" "$(cat "$root/unl")"
grep -qE 'not in writer form: *[0-9]+|[0-9]+ headings not in writer form' "$root/unl" && ok "test 6: the headings not in writer form are counted" || bad "test 6: the count of legacy headings" "$(cat "$root/unl")"

# --- test 7: refresh never makes a judging move ----------------------------------------------
fresh_state
open_at 2026-10-05T10:00:00Z j1 --title "judge me" --owner director --stage "in review" --link pr:o/r#1
entry 3000 2026-10-18T10:00:00Z agent://t/z j1
# Sources that suggest each judging move: a PR link on an in-review row, a relay entry whose
# header and text say accepted, parked, released and deployed, and a Desk card that is closed
# on a row blocked on the operator (the held LF4 move).
open_at 2026-10-05T10:00:00Z j2 --title "blocked on a closed card" --owner director --blocked operator --ask c9 --link ask:c9 2>/dev/null || true
cat >>"$LEDGER" <<'J'
{"ts":"2026-10-05T10:00:00Z","actor":"director","tag":"judgment","event":"open","ws":"j2","title":"blocked on a closed card","owner":"director","blocked":"operator","links":["ask:c9"]}
J
{ echo; echo "## R-3001 (2026-10-18T11:00:00Z) -> agent://t/accepted-parked-released-deployed"; echo "**Workstream:** j1, j2"; echo "**Text, verbatim:**"; echo "> accepted, parked, released, deployed, merged, open a new row"; echo "**Outcome:** pending"; } >>"$RELAY_LOG"
printf '{"c9": {"status": "closed"}}' >"$root/cards7.json"
DWS_NOW=2099-01-01T00:00:00Z "$SNAP" write --cards "$root/cards7.json" --reported 1 >/dev/null
NOW=2099-01-01T00:00:00Z refresh_local >/dev/null 2>&1
[[ "$(field j2 blocked)" == "operator" ]] && ok "test 7: a closed linked card does not clear the operator block (LF4 is held)" || bad "test 7: the block was cleared" "$(field j2 blocked)"
[[ "$(field j1 stage)" == "in review" ]] && ok "test 7: no stage moved on any suggestion" || bad "test 7: a stage moved" "$(field j1 stage)"
python3 - "$LEDGER" <<'P' && ok "test 7: the fill appends observe events only (no stage, park, open, set or link)" || bad "test 7: the fill appended a judging event"
import json, sys
ev = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
bad = [e["event"] for e in ev if e["actor"] == "refresh" and e["event"] != "observe"]
assert not bad, bad
assert all(e["actor"] == "refresh" and e["tag"] == "mirror" for e in ev if "src" in e), ev
P
# the regression pin, which already passes on main: refresh may not make these moves
for cmd in "stage j1 accepted --from in-review" "park j1 --from in-review" "open j9 --title x" "set j1 --next no" "link j1 pr:o/r#9"; do
  n="$(lines)"
  if "$DWS" --actor refresh $cmd >"$root/out" 2>&1; then bad "test 7 pin: --actor refresh $cmd is refused" "accepted"
  elif [[ "$(lines)" != "$n" ]]; then bad "test 7 pin: --actor refresh $cmd" "appended anyway"
  else ok "test 7 pin: --actor refresh $cmd is refused and appends nothing"; fi
done

# --- test 8: an after-the-fact link moves the row once --------------------------------------
fresh_state
open_at 2026-10-05T10:00:00Z f2 --title "late link" --owner director
entry 2601 2026-10-17T08:00:00Z agent://t/q
"$DWS" link f2 relay:R-2601 >/dev/null 2>&1
n="$(lines)"
refresh_local >/dev/null 2>&1
[[ "$(lines)" == "$((n + 1))" && "$(field f2 last_moved)" == "2026-10-17T08:00:00Z" ]] && ok "test 8: dws link <slug> relay:R-<n> then refresh moves the row once" || bad "test 8: late link" "$n -> $(lines)"
refresh_local >/dev/null 2>&1
[[ "$(lines)" == "$((n + 1))" ]] && ok "test 8: a second refresh moves nothing" || bad "test 8: replay"

# --- an outcome note in writer form moves the row its entry is linked to ----------------------
fresh_state
open_at 2026-10-05T10:00:00Z f3 --title "outcome" --owner director
entry 2610 2026-10-17T08:00:00Z agent://t/q f3
printf '\n### R-2610 outcome (2026-10-18T07:00:00Z)\npeer answered\n' >>"$RELAY_LOG"
refresh_local >/dev/null 2>&1
[[ "$(field f3 last_moved)" == "2026-10-18T07:00:00Z" ]] && ok "an outcome note moves the row its entry is linked to, dated by the note" || bad "outcome note" "$(field f3 last_moved)"
python3 - "$(field f3 consumed | tr -d "[]'" | tr ',' ' ')" <<'P' && ok "the outcome has its own src, apart from the entry" || bad "the outcome src"
import sys
t = sys.argv[1]
assert "relay:R-2610/outcome@2026-10-18T07:00:00Z" in t and "relay:R-2610@2026-10-17T08:00:00Z" in t, t
P

# --- test 10: the Desk half reads UNREAD without a fresh snapshot -----------------------------
fresh_state
open_at 2026-10-05T10:00:00Z d1 --title "desk row" --owner director --link ask:c1
n="$(lines)"
refresh_local >/dev/null 2>&1
[[ "$(lines)" == "$n" ]] && ok "test 10: with no snapshot no Desk event is written" || bad "test 10: no snapshot"
"$DWS" unlinked >"$root/unl" 2>&1
grep -qi 'desk.*UNREAD' "$root/unl" && ok "test 10: unlinked says UNREAD for the Desk" || bad "test 10: UNREAD for the Desk" "$(cat "$root/unl")"
printf '{"c1": {"status": "open"}, "c2": {"status": "open"}}' >"$root/cards.json"
DWS_NOW=2026-10-17T10:00:00Z "$SNAP" write --cards "$root/cards.json" --reported 2 >/dev/null
refresh_local >/dev/null 2>&1
[[ "$(lines)" == "$n" ]] && ok "test 10: a snapshot older than 24 hours writes no Desk event" || bad "test 10: old snapshot"
"$DWS" unlinked >"$root/unl" 2>&1
grep -qi 'desk.*UNREAD' "$root/unl" && ok "test 10: and unlinked still says UNREAD" || bad "test 10: old snapshot UNREAD" "$(cat "$root/unl")"
# a fresh snapshot: one observe for the linked open card, once
DWS_NOW=2026-10-19T10:00:00Z "$SNAP" write --cards "$root/cards.json" --reported 2 >/dev/null
refresh_local >/dev/null 2>&1
python3 - "$LEDGER" <<'P' && ok "the Desk reader records one observe for an open linked card, with its src and read time" || bad "the Desk observe" "$(last_event)"
import json, sys
ev = [json.loads(l) for l in open(sys.argv[1]) if l.strip()][-1]
assert ev["event"] == "observe" and ev["link"] == "ask:c1" and ev["actor"] == "refresh" and ev["tag"] == "mirror", ev
assert ev["src"] == "desk:c1@2026-10-19T10:00:00Z" and ev["seen"]["state"] == "open", ev
P
n="$(lines)"; refresh_local >/dev/null 2>&1
[[ "$(lines)" == "$n" ]] && ok "the Desk reader writes nothing on a replay of the same snapshot" || bad "Desk replay"
"$DWS" unlinked >"$root/unl" 2>&1
grep -q 'c2' "$root/unl" && ok "unlinked lists an open Desk card that no row links" || bad "unlinked: an open card with no row" "$(cat "$root/unl")"

# --- unlinked: rows with no link, and rows blocked on the operator with no card -----------------
fresh_state
open_at 2026-10-05T10:00:00Z n1 --title "no link at all" --owner director
cat >>"$LEDGER" <<'J'
{"ts":"2026-10-06T00:00:00Z","actor":"director","event":"open","ws":"ob","title":"old operator block","owner":"x","blocked":"operator"}
J
"$DWS" unlinked >"$root/unl" 2>&1
grep -q 'n1' "$root/unl" && ok "unlinked lists a row with no link" || bad "unlinked: a row with no link" "$(cat "$root/unl")"
grep -A1 'blocked on the operator' "$root/unl" | grep -q 'ob'   && ok "unlinked lists a row blocked on the operator with no card link" || bad "unlinked: operator block with no card" "$(cat "$root/unl")"
"$DWS" unlinked --json >"$root/unl.json" 2>&1 && python3 -c 'import json,sys;json.load(open(sys.argv[1]))' "$root/unl.json" && ok "unlinked --json is JSON" || bad "unlinked --json is JSON" "$(cat "$root/unl.json")"
"$DWS" unlinked >/dev/null 2>&1; n="$(lines)"; "$DWS" unlinked >/dev/null 2>&1
[[ "$(lines)" == "$n" ]] && ok "unlinked writes nothing" || bad "unlinked writes nothing"

# --- test 12: --local reaches no network ------------------------------------------------------------
# Two instruments: the stubbed gh and bd, which see a subprocess, and the socket guard, which sees
# any connect or name lookup made from python itself. Rows carry pr, issue and bd links, a Desk
# card and a relay entry, so every source the fill could reach is suggested to it.
fresh_state
open_at 2026-10-05T10:00:00Z g1 --title "net" --owner director --link pr:o/r#1 bd:aae-orc-zz1 issue:o/r#2 ask:c1
entry 2700 2026-10-18T10:00:00Z agent://t/n g1
printf '{"c1": {"status": "open"}}' >"$root/cards12.json"
DWS_NOW=2099-01-01T00:00:00Z "$SNAP" write --cards "$root/cards12.json" --reported 1 >/dev/null
: >"$CALLS"; : >"$NETLOG"
NOW=2099-01-01T00:00:00Z netrun bash -c 'DWS_NOW=2099-01-01T00:00:00Z "$0" refresh --local' "$DWS" >/dev/null 2>&1
[[ ! -s "$CALLS" ]] && ok "test 12: the stubbed gh and bd were never called by refresh --local" || bad "test 12: subprocess network calls" "$(cat "$CALLS")"
[[ ! -s "$NETLOG" ]] && ok "test 12: no connect or name lookup was made from python" || bad "test 12: socket-layer network use" "$(cat "$NETLOG")"
[[ "$(field g1 last_moved)" == "2026-10-18T10:00:00Z" ]] && ok "test 12: and the local sources still moved the row" || bad "test 12: positive" "$(field g1 last_moved)"

# test 12 control: the detector must see a connect that goes through no stubbed binary. A copy of
# dws with a urllib connect planted at the top of the local fill is run the way the real one is;
# the detection used above must flag it, or test 12 proves nothing about a library call.
mkdir -p "$root/plant"
python3 - "$DWS" "$root/plant/dws" <<'P'
import sys
t = open(sys.argv[1]).read()
hook = "def cmd_refresh_local(a):\n"
assert hook in t
planted = hook + "    import urllib.request\n    try:\n        urllib.request.urlopen('http://127.0.0.1:9/', timeout=1)\n    except Exception:\n        pass\n"
open(sys.argv[2], "w").write(t.replace(hook, planted, 1))
P
chmod +x "$root/plant/dws"
fresh_state
open_at 2026-10-05T10:00:00Z pl1 --title "plant" --owner director
entry 2701 2026-10-18T10:00:00Z agent://t/n pl1
: >"$CALLS"
: >"$NETLOG"
DWS_NOW=2099-01-01T00:00:00Z netrun "$root/plant/dws" refresh --local >"$root/plant.out" 2>&1 || true
[[ -s "$NETLOG" ]] && ok "test 12 control: the socket guard flags a planted urllib connect" || bad "test 12 control: a planted urllib connect goes unseen by the socket guard" "$(cat "$root/plant.out")"

# --- test 13: no render loop -------------------------------------------------------------------------
fresh_state
mkdir -p "$root/copy"
cp "$DWS" "$root/copy/dws"
count="$root/render-count"; : >"$count"
printf '#!/bin/sh\necho x >>"%s"\n' "$count" >"$root/copy/board-html"; chmod +x "$root/copy/board-html"
open_at 2026-10-05T10:00:00Z r1 --title "render" --owner director
printf '<html></html>' >"$DIRECTOR_STATE/board.html"
entry 2800 2026-10-18T10:00:00Z agent://t/r r1
: >"$count"
DWS_NO_RERENDER=1 DWS_NOW=2099-01-01T00:00:00Z "$root/copy/dws" refresh --local >/dev/null 2>&1
[[ "$(wc -l <"$count" | tr -d ' ')" == 0 && "$(field r1 last_moved)" == "2026-10-18T10:00:00Z" ]] && ok "test 13: a fill launched as the renderer launches it (DWS_NO_RERENDER=1) re-renders zero times, and still wrote" || bad "test 13: no re-render" "$(cat "$count")"
entry 2801 2026-10-18T11:00:00Z agent://t/r r1
: >"$count"
DWS_NOW=2099-01-01T00:00:00Z "$root/copy/dws" refresh --local >/dev/null 2>&1
[[ "$(wc -l <"$count" | tr -d ' ')" == 1 ]] && ok "test 13 control: without DWS_NO_RERENDER the same write re-renders once" || bad "test 13 control: re-render once" "$(wc -l <"$count")"

# --- test 14: a held relay lock is reported, the Desk is still read ------------------------------------
fresh_state
open_at 2026-10-05T10:00:00Z h1 --title "lock" --owner director --link ask:c1
entry 2900 2026-10-18T10:00:00Z agent://t/l h1
DWS_NOW=2026-10-19T10:00:00Z "$SNAP" write --cards "$root/cards.json" --reported 2 >/dev/null
python3 - "$RELAY_LOG.lock" <<'P' &
import fcntl, sys, time
f = open(sys.argv[1], "a")
fcntl.flock(f, fcntl.LOCK_EX)
time.sleep(6.5)
P
holder=$!
sleep 1
t0=$SECONDS
refresh_local >"$root/out" 2>&1 || true
took=$((SECONDS - t0))
wait "$holder" || true
grep -qi 'relay.*unreadable' "$root/out" && ok "test 14: a held relay lock reports the relay source unreadable" || bad "test 14: unreadable report" "$(cat "$root/out")"
[[ "$took" -le 7 && "$took" -ge 4 ]] && ok "test 14: it waited about the 5 s timeout, no longer ($took s)" || bad "test 14: timing" "$took s"
python3 - "$LEDGER" <<'P' && ok "test 14: no relay event was written, and the Desk was still read" || bad "test 14: events" "$(cat "$LEDGER")"
import json, sys
ev = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
assert not [e for e in ev if str(e.get("src", "")).startswith("relay:")], ev
assert [e for e in ev if str(e.get("src", "")).startswith("desk:c1@")], ev
P

echo "dws-fill: $pass passed, $fail failed"
[[ "$fail" == 0 ]]

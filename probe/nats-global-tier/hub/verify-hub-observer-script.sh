#!/usr/bin/env bash
# Prove the at-host script sim/design/2026-11-13-hub-observer-user.sh against SCRATCH
# hubs, never a live one. The script changes the live hub when the operator runs it
# on kinu, so its checks and its failure path have to be trustworthy first:
#
#   happy path     it adds the user, reloads, runs its reads and its negatives,
#                  and the inserted grant is the template's observer.director block
#                  with the key filled in and nothing else;
#   neg() strict   a negative that is wrongly allowed fails the script. Two scratch
#                  mutations of the grant: MSG.NEXT allowed (silent, no violation,
#                  which the old check passed) and STREAM.MSG.DELETE allowed
#                  (answered);
#   rollback       a failure after the reload prints a ROLLBACK line, and running
#                  that line puts the scratch conf back and the hub still reloads.
#
# Each case has its own hub, home directory and ports. The script under test is run
# as the operator would run it, with DIRECTOR_GLOBAL_HOME, HUB_URL and
# OBSERVER_TEMPLATE set. `nk`, which kinu has and a scratch host may not, is a shim
# over testdata/nkgen.go. Needs nats-server, the nats CLI, jq, python3 and go on PATH.
#
# Run it as the relay run conditions ask (.claude/rules/relay-run-conditions.md), and
# with a bash that honors TMPDIR (bash 3.2 writes here-document temp files to /tmp,
# which a write-deny profile refuses). HOB_TRACE=1 runs the script under test with
# bash -x. Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE="$here/nats-server.conf"
SCRIPT="$here/../../../sim/design/2026-11-13-hub-observer-user.sh"
SHIM_SRC="$here/../../../cmd/director-mcp"

for c in nats-server nats jq python3 go; do
  command -v "$c" >/dev/null || { echo "$c not on PATH"; exit 2; }
done
[[ -f "$SCRIPT" ]] || { echo "missing $SCRIPT"; exit 2; }

work="$(mktemp -d "${TMPDIR:-/tmp}/hobscript.XXXXXX")"
pids=()
cleanup() {
  local p
  for p in ${pids[@]+"${pids[@]}"}; do
    if kill -0 "$p" 2>/dev/null; then kill "$p" 2>/dev/null || true; fi
  done
  rm -rf "$work"
}
trap cleanup EXIT

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])'; }

# nk shim: `nk -gen user` prints a seed; `nk -inkey FILE -pubout` prints its public key.
( cd "$SHIM_SRC" && go build -o "$work/nkgen" testdata/nkgen.go )
mkdir -p "$work/bin"
cat >"$work/bin/nk" <<SHIM
#!/usr/bin/env bash
set -euo pipefail
if [[ "\$1" == "-gen" && "\$2" == "user" ]]; then
  t="\$(mktemp -u "$work/nkseed.XXXXXX")"; "$work/nkgen" "\$t" >/dev/null; cat "\$t"; rm -f "\$t"
elif [[ "\$1" == "-inkey" && "\$3" == "-pubout" ]]; then
  "$work/nkgen" -pub "\$2"
else
  echo "nk shim: unsupported arguments: \$*" >&2; exit 2
fi
SHIM
chmod +x "$work/bin/nk"
export PATH="$work/bin:$PATH"

# new_hub NAME: a scratch "global home" shaped like the live one (conf with one
# `users: [` line, keys/admin.nk, pid file, log) and a running hub with one stream,
# one durable and some mail. Sets H, URL, ADMIN, and starts the hub.
new_hub() {
  H="$work/$1"; mkdir -p "$H/keys" "$H/log" "$H/store"
  local admin_pub p
  admin_pub="$("$work/nkgen" "$H/keys/admin.nk")"
  p="$(port)"; URL="nats://127.0.0.1:$p"
  cat >"$H/nats-server.conf" <<CONF
listen: 127.0.0.1:$p
jetstream { store_dir: "$H/store", domain: global }
accounts {
  FLEET {
    jetstream: enabled
    users: [
      { nkey: $admin_pub }
    ]
  }
}
CONF
  nats-server -c "$H/nats-server.conf" --pid "$H/nats-server.pid" -l "$H/log/nats-server.log" &
  pids+=($!)
  ADMIN=(nats -s "$URL" --nkey "$H/keys/admin.nk" --timeout 5s)
  for _ in $(seq 1 50); do "${ADMIN[@]}" account info >/dev/null 2>&1 && break; sleep 0.2; done
  "${ADMIN[@]}" account info >/dev/null 2>&1 || { echo "scratch hub $1 did not come up"; cat "$H/log/nats-server.log"; exit 2; }
  "${ADMIN[@]}" stream add GLOBAL_TO_mokuzai --subjects 'global.mokuzai.>' --storage file --retention limits --defaults >/dev/null
  "${ADMIN[@]}" consumer add GLOBAL_TO_mokuzai mcp_global_mok_01AAA --pull --filter global.mokuzai.supervisor.inbox --ack explicit --deliver all --defaults >/dev/null
  "${ADMIN[@]}" pub global.mokuzai.supervisor.inbox m --count 3 >/dev/null
}

# run_script TEMPLATE: the script as the operator runs it. Output in $work/out, status in $RC.
run_script() {
  RC=0
  DIRECTOR_GLOBAL_HOME="$H" HUB_URL="$URL" OBSERVER_TEMPLATE="$1" bash ${HOB_TRACE:+-x} "$SCRIPT" >"$work/out" 2>&1 || RC=$?
}

# --- case A: the happy path, and the grant equals the template --------------------------
new_hub a
run_script "$TEMPLATE"
[[ "$RC" == 0 ]] && ok "A: the script runs to the end on a scratch hub (exit 0)" || bad "A: exit $RC" "$(tail -12 "$work/out" | tr "\n" ";")"
[[ "$(grep -c '^ok, refused with a permission violation' "$work/out")" == 4 ]] \
  && ok "A: all four negatives are refused with a permission violation naming their subject" || bad "A: negatives" "$(grep -E 'refused|FAIL' "$work/out" | tr '\n' ';')"
grep -q 'consumer counts unchanged' "$work/out" && ok "A: consumer counts are unchanged" || bad "A: consumer counts"
pub="$(sed -n 's/^public key: //p' "$work/out")"
# The inserted block: from the "observer.director (I-469)" comment through the line before
# the next top-level user or the closing bracket; compared with the template's block, key
# filled in, whitespace removed.
got="$(awk '/# observer.director \(I-469\)/{f=1} f{print} f && /^ *} }$/{exit}' "$H/nats-server.conf" | grep -v '^ *#' | tr -d ' \n')"
want="$(awk '/# BEGIN observer.director/{f=1;next} /# END observer.director/{f=0} f{ sub(/^[[:space:]]*# ?/,""); print }' "$TEMPLATE" | grep -v '^ *#' | sed "s/OBSERVER_DIRECTOR_PUBLIC_KEY/$pub/" | tr -d ' \n')"
if [[ -n "$got" && "$got" == "$want" ]]; then
  ok "A: the grant inserted into the conf equals the template's observer.director block, key filled in"
else
  bad "A: grant differs from the template" "got=[${got:0:120}] want=[${want:0:120}]"
fi
grep -q "$(printf '%s' "$pub")" "$H/nats-server.conf" && [[ -n "$pub" ]] && ok "A: the new public key is in the conf" || bad "A: public key not in conf"
run_script "$TEMPLATE"
[[ "$RC" != 0 ]] && grep -q 'already in' "$work/out" && ok "A: a second run stops because the user already exists" || bad "A: second run" "rc=$RC"

# --- case B: neg() fails on a wrongly allowed negative ----------------------------------
mutate() { # new-template-path, python expression text-to-text
  python3 - "$TEMPLATE" "$1" "$2" <<'PY'
import sys
src, dst, mode = sys.argv[1:4]
t = open(src).read()
if mode == "allow-msg-next":
    # MSG.NEXT leaves the deny list and joins the allow list: allowed, and silent on a missing durable.
    a = '"$JS.API.CONSUMER.MSG.NEXT.>",'
    assert t.count(a) == 1
    t = t.replace(a, "")
    b = '"$JS.API.CONSUMER.INFO.*.*",'
    assert t.count(b) == 1
    t = t.replace(b, b + ' "$JS.API.CONSUMER.MSG.NEXT.*.*",')
elif mode == "allow-msg-delete":
    a = '"$JS.API.STREAM.MSG.DELETE.>",'
    assert t.count(a) == 1
    t = t.replace(a, "")
    b = '"$JS.API.STREAM.MSG.GET.*",'
    assert t.count(b) == 1
    t = t.replace(b, b + ' "$JS.API.STREAM.MSG.DELETE.*",')
elif mode == "drop-stream-names":
    a = '"$JS.API.STREAM.NAMES",'
    assert t.count(a) == 1
    t = t.replace(a, "")
else:
    raise SystemExit("unknown mode " + mode)
open(dst, "w").write(t)
PY
}
mutate "$work/t-msgnext.conf" allow-msg-next
new_hub b1
run_script "$work/t-msgnext.conf"
if [[ "$RC" != 0 ]] && grep -q 'FAIL: .*CONSUMER.MSG.NEXT.*no permission violation' "$work/out"; then
  ok "B1: with MSG.NEXT wrongly allowed the script fails ($(grep -m1 'FAIL: ' "$work/out" | cut -c1-110))"
else
  bad "B1: MSG.NEXT allowed but the script did not fail on it" "rc=$RC $(grep -E 'refused|FAIL' "$work/out" | tr '\n' ';')"
fi
mutate "$work/t-msgdel.conf" allow-msg-delete
new_hub b2
run_script "$work/t-msgdel.conf"
if [[ "$RC" != 0 ]] && grep -q 'FAIL: .*STREAM.MSG.DELETE.*answered' "$work/out"; then
  ok "B2: with STREAM.MSG.DELETE wrongly allowed the script fails ($(grep -m1 'FAIL: ' "$work/out" | cut -c1-100))"
else
  bad "B2: MSG.DELETE allowed but the script did not fail on it" "rc=$RC $(grep -E 'refused|FAIL' "$work/out" | tr '\n' ';')"
fi

# --- case C: a failure after the reload prints ROLLBACK, and the line works --------------
mutate "$work/t-nonames.conf" drop-stream-names
new_hub c
cp -p "$H/nats-server.conf" "$work/c.conf.original"
run_script "$work/t-nonames.conf"
if [[ "$RC" != 0 ]] && grep -q '^ROLLBACK: cp -p ' "$work/out"; then
  ok "C: a post-reload failure (the observer cannot list streams) exits nonzero and prints a ROLLBACK line"
else
  bad "C: no ROLLBACK after a post-reload failure" "rc=$RC $(tail -4 "$work/out" | tr '\n' ';')"
fi
cmd="$(sed -n 's/^ROLLBACK: //p' "$work/out" | head -n 1)"
if [[ -n "$cmd" ]] && grep -q 'observer.director' "$H/nats-server.conf"; then
  ok "C: the conf holds the new user before the rollback"
else
  bad "C: the conf was not changed before the rollback, or no command"
fi
if [[ -n "$cmd" ]] && bash -c "$cmd" >/dev/null 2>&1; then
  sleep 2
  if cmp -s "$H/nats-server.conf" "$work/c.conf.original" && [[ ! -e "$H/keys/observer-director.nk" ]]; then
    ok "C: running the ROLLBACK line restores the conf byte for byte and removes the seed"
  else
    bad "C: rollback did not restore the conf or left the seed"
  fi
  "${ADMIN[@]}" stream ls -n 2>/dev/null | grep -qx GLOBAL_TO_mokuzai \
    && ok "C: the hub still answers after the rollback reload" || bad "C: hub does not answer after the rollback"
else
  bad "C: the ROLLBACK line failed to run" "$cmd"
fi

echo "---"
echo "$pass passed, $fail failed"
[[ "$fail" == 0 ]]

#!/usr/bin/env bash
# Prove the hub observer (I-469, sim/design/hub-observer.md) on a scratch hub
# with two leaves. Nothing live is touched: the hub, both leaves and every key
# are made here, on localhost ports this script picks, and removed on exit.
#
# What it shows:
#   positive  the observer lists every hub stream, lists a stream's durables,
#             reads a durable's counts and gets a stored message;
#   negative  it cannot create a consumer, pull (MSG.NEXT), delete a consumer,
#             delete a message or publish to a seat's inbox, and the hub's
#             state (consumer counts, each durable's pending, ack-pending, ack
#             floor and delivered, each stream's message count) is the same
#             after those attempts;
#   leaves    each leaf's user still reaches only its own cluster, so reading
#             the others needed the observer and no leaf was widened;
#   shim      `director-mcp unread --global` run beside one leaf reads every
#             stream in full with the observer, reads its own cluster only
#             without it, and names a GAP when the observer cannot be used.
#
# The observer's permissions are read from the observer.director block of the hub
# conf template (nats-server.conf), so a grant that drifts from the template
# fails here. The positive and negative checks need only that block; the shim
# check needs the observer read path in director-mcp.
#
# Needs nats-server, the nats CLI, jq, python3 and go on PATH. Usage, from anywhere:
#   probe/nats-global-tier/hub/verify-hub-observer.sh
# Run it as the relay run conditions ask (.claude/rules/relay-run-conditions.md):
# `env -i` with a scratch HOME and a short TMPDIR, no MARVEL_SOCKET, localhost
# only under a sandbox-exec profile, and proof the real marvel home was not
# written. Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE="$here/nats-server.conf"
SHIM_SRC="$here/../../../cmd/director-mcp"

for c in nats-server nats jq python3 go; do
  command -v "$c" >/dev/null || { echo "$c not on PATH"; exit 2; }
done

work="$(mktemp -d "${TMPDIR:-/tmp}/hobs.XXXXXX")"
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
HUBP="$(port)"; LEAFP="$(port)"; L1P="$(port)"; L2P="$(port)"
HUB="nats://127.0.0.1:$HUBP"

gen() { ( cd "$SHIM_SRC" && go run testdata/nkgen.go "$work/$1.nk" ); }
ADMIN_PUB="$(gen admin)"; OBS_PUB="$(gen observer)"; KINU_PUB="$(gen leaf-kinu)"; MOK_PUB="$(gen leaf-mokuzai)"

observer_block() {
  awk '/# BEGIN observer.director/{f=1;next} /# END observer.director/{f=0} f{ sub(/^[[:space:]]*# ?/,""); print }' "$TEMPLATE" \
    | sed "s/OBSERVER_DIRECTOR_PUBLIC_KEY/$OBS_PUB/"
}
OBSBLOCK="$(observer_block)"
if [[ -z "$OBSBLOCK" ]]; then echo "the template has no observer.director block"; exit 2; fi

# A leaf user may read and consume only its own cluster's stream and read
# presence, as in the template. The observer is the only user that may list.
leaf_user() { # public key, stream, publish-subject
  cat <<USER
      { nkey: $1
        permissions {
          publish { allow: [ "$3", "global.director.>",
                             "\$JS.global.API.STREAM.INFO.$2", "\$JS.global.API.CONSUMER.INFO.$2.>",
                             "\$JS.global.API.INFO", "\$JS.global.API.STREAM.INFO.KV_GLOBAL_PRESENCE",
                             "\$JS.global.API.DIRECT.GET.KV_GLOBAL_PRESENCE.>", "\$JS.ACK.>" ] }
          subscribe { allow: [ "global.director.>", "_INBOX.>" ] }
        } }
USER
}

mkdir -p "$work/hub"
cat >"$work/hub.conf" <<CONF
server_name: verify-hub-observer
listen: 127.0.0.1:$HUBP
jetstream { store_dir: "$work/hub", domain: global }
leafnodes { listen: 127.0.0.1:$LEAFP }
accounts {
  FLEET {
    jetstream: enabled
    users: [
      { nkey: $ADMIN_PUB }
$(leaf_user "$KINU_PUB" GLOBAL_TO_DIRECTOR "global.*.supervisor.inbox")
$(leaf_user "$MOK_PUB" GLOBAL_TO_mokuzai "global.director.inbox")
$OBSBLOCK
    ]
  }
}
CONF
nats-server -c "$work/hub.conf" -D -l "$work/hub.log" &
pids+=($!)
for _ in $(seq 1 50); do
  nats -s "$HUB" --nkey "$work/admin.nk" account info >/dev/null 2>&1 && break
  sleep 0.2
done
nats -s "$HUB" --nkey "$work/admin.nk" account info >/dev/null 2>&1 || { echo "scratch hub did not come up; see $work/hub.log"; cat "$work/hub.log"; exit 2; }

ADMIN=(nats -s "$HUB" --nkey "$work/admin.nk" --timeout 5s)
OBS=(nats -s "$HUB" --nkey "$work/observer.nk" --inbox-prefix _INBOX_observer_director --timeout 3s)

# --- seed the hub as admin: three streams, presence, a durable per seat ------------
for s in DIRECTOR:director mokuzai:mokuzai kinu:kinu; do
  name="GLOBAL_TO_${s%%:*}"; subj="global.${s##*:}.>"
  "${ADMIN[@]}" stream add "$name" --subjects "$subj" --storage file --retention limits --defaults >/dev/null
done
"${ADMIN[@]}" kv add GLOBAL_PRESENCE --ttl 90s >/dev/null
"${ADMIN[@]}" consumer add GLOBAL_TO_mokuzai mcp_global_mok-sup_01AAA --pull --filter global.mokuzai.supervisor.inbox --ack explicit --deliver all --defaults >/dev/null
"${ADMIN[@]}" consumer add GLOBAL_TO_kinu mcp_global_kinu-sup_01BBB --pull --filter global.kinu.supervisor.inbox --ack explicit --deliver all --defaults >/dev/null
"${ADMIN[@]}" consumer add GLOBAL_TO_DIRECTOR mcp_global_dir_01CCC --pull --filter global.director.inbox --ack explicit --deliver all --defaults >/dev/null
"${ADMIN[@]}" pub global.mokuzai.supervisor.inbox m --count 3 >/dev/null
"${ADMIN[@]}" pub global.kinu.supervisor.inbox m --count 2 >/dev/null
"${ADMIN[@]}" pub global.director.inbox m --count 4 >/dev/null
# One message pulled and acked on the mokuzai durable, so its floor is not zero. This is
# also the admin control for MSG.NEXT: the same call the observer is refused below succeeds
# for a user that is granted it.
"${ADMIN[@]}" consumer next GLOBAL_TO_mokuzai mcp_global_mok-sup_01AAA --ack >/dev/null
# Scratch targets for the other four refusals, each with its admin control run once here and
# succeeding, so a refusal below cannot be a malformed command: CONSUMER.CREATE (admin adds a
# durable), CONSUMER.DELETE (admin removes a throwaway one), STREAM.MSG.DELETE (admin deletes
# a message) and STREAM.PURGE (admin purges a second scratch stream). The observer is then
# refused the same four on the first stream, against targets that still exist.
"${ADMIN[@]}" stream add GLOBAL_TO_probe --subjects 'global.probe.>' --storage file --retention limits --defaults >/dev/null
"${ADMIN[@]}" stream add GLOBAL_TO_probe2 --subjects 'global.probe2.>' --storage file --retention limits --defaults >/dev/null
"${ADMIN[@]}" pub global.probe.inbox m --count 4 >/dev/null
"${ADMIN[@]}" pub global.probe2.inbox m --count 2 >/dev/null
"${ADMIN[@]}" consumer add GLOBAL_TO_probe admin-created --pull --defaults >/dev/null \
  && ok "admin control: CONSUMER.CREATE succeeds for a user that is granted it" || bad "admin control: CONSUMER.CREATE"
"${ADMIN[@]}" consumer add GLOBAL_TO_probe admin-doomed --pull --defaults >/dev/null
"${ADMIN[@]}" consumer rm GLOBAL_TO_probe admin-doomed -f >/dev/null \
  && ok "admin control: CONSUMER.DELETE succeeds for a user that is granted it" || bad "admin control: CONSUMER.DELETE"
"${ADMIN[@]}" stream rmm GLOBAL_TO_probe 4 -f >/dev/null \
  && ok "admin control: STREAM.MSG.DELETE succeeds for a user that is granted it" || bad "admin control: STREAM.MSG.DELETE"
"${ADMIN[@]}" stream purge GLOBAL_TO_probe2 -f >/dev/null \
  && ok "admin control: STREAM.PURGE succeeds for a user that is granted it" || bad "admin control: STREAM.PURGE"

# state: every stream's consumer count and message count, every durable's counts and floors.
state() {
  local s d
  for s in $("${ADMIN[@]}" stream ls -n); do
    echo "stream $s $("${ADMIN[@]}" stream info "$s" -j | jq -c '{consumers: .state.consumer_count, messages: .state.messages, last: .state.last_seq}')"
    for d in $("${ADMIN[@]}" consumer ls "$s" -n); do
      echo "durable $s/$d $("${ADMIN[@]}" consumer info "$s" "$d" -j | jq -c '{pending: .num_pending, ack_pending: .num_ack_pending, ack_floor: .ack_floor.stream_seq, delivered: .delivered.stream_seq}')"
    done
  done | LC_ALL=C sort
}
state >"$work/state.before"

# --- positive: what the observer may do --------------------------------------------
names="$("${OBS[@]}" stream ls -n 2>"$work/err" || true)"
for s in GLOBAL_TO_DIRECTOR GLOBAL_TO_mokuzai GLOBAL_TO_kinu KV_GLOBAL_PRESENCE; do
  grep -qx "$s" <<<"$names" && ok "positive: the observer lists $s" || bad "positive: list $s" "$(cat "$work/err")"
done
"${OBS[@]}" consumer ls GLOBAL_TO_mokuzai -n 2>/dev/null | grep -qx mcp_global_mok-sup_01AAA \
  && ok "positive: the observer lists a stream's durables" || bad "positive: consumer ls"
pend="$("${OBS[@]}" consumer info GLOBAL_TO_mokuzai mcp_global_mok-sup_01AAA -j 2>/dev/null | jq -r '.num_pending' || true)"
[[ "$pend" == 2 ]] && ok "positive: CONSUMER.INFO shows the durable's pending (2)" || bad "positive: consumer info" "num_pending=$pend"
"${OBS[@]}" stream get GLOBAL_TO_mokuzai 1 >/dev/null 2>&1 \
  && ok "positive: MSG.GET returns a stored message" || bad "positive: stream get"

# --- negative: what it may not do --------------------------------------------------
# A refusal counts only when the command fails AND the hub logs a permission violation that
# names this attempt's own subject (the hub runs with -D, so the log carries it). A failure for
# any other reason, or a violation logged for another attempt, does not pass.
refused() { # label, subject ERE, command...
  local label="$1" subj="$2"; shift 2
  local n0; n0="$(wc -l <"$work/hub.log")"
  if "$@" >/dev/null 2>"$work/neg.err"; then bad "negative: $label was answered (it must be refused)"; return; fi
  sleep 0.5
  if tail -n +"$((n0+1))" "$work/hub.log" | grep -i 'violation' | grep -Eq "$subj"; then
    ok "negative: $label is refused, and the hub logged a permission violation on its own subject"
  else
    bad "negative: $label failed without a permission violation naming its subject" "stderr: $(head -c 160 "$work/neg.err" | tr '\n' ' ')"
  fi
}
refused "CONSUMER.CREATE (new durable)" '\$JS\.API\.CONSUMER\.(DURABLE\.)?CREATE\.GLOBAL_TO_probe' "${OBS[@]}" consumer add GLOBAL_TO_probe observer-check --pull --defaults
refused "MSG.NEXT (pull from a seat's durable)" '\$JS\.API\.CONSUMER\.MSG\.NEXT\.GLOBAL_TO_mokuzai\.mcp_global_mok-sup_01AAA' "${OBS[@]}" consumer next GLOBAL_TO_mokuzai mcp_global_mok-sup_01AAA --ack
refused "CONSUMER.DELETE" '\$JS\.API\.CONSUMER\.DELETE\.GLOBAL_TO_probe\.admin-created' "${OBS[@]}" consumer rm GLOBAL_TO_probe admin-created -f
refused "STREAM.MSG.DELETE" '\$JS\.API\.STREAM\.MSG\.DELETE\.GLOBAL_TO_probe' "${OBS[@]}" stream rmm GLOBAL_TO_probe 1 -f
refused "STREAM.PURGE" '\$JS\.API\.STREAM\.PURGE\.GLOBAL_TO_probe' "${OBS[@]}" stream purge GLOBAL_TO_probe -f
# A publish returns no error to the client; the hub's state shows whether it landed.
"${OBS[@]}" pub global.director.inbox observer-check >/dev/null 2>&1 || true
sleep 1
state >"$work/state.after"
if cmp -s "$work/state.before" "$work/state.after"; then
  ok "state: consumer counts, message counts, pending, ack floors and delivered are unchanged after every attempt"
else
  bad "state changed" "$(diff "$work/state.before" "$work/state.after" | head -8 | tr '\n' ';')"
fi

# --- leaves: no leaf was widened ---------------------------------------------------
mkdir -p "$work/l1" "$work/l2"
start_leaf() { # name port seedfile
  cat >"$work/$1.conf" <<CONF
server_name: verify-$1
listen: 127.0.0.1:$2
jetstream { store_dir: "$work/$1", domain: verify$1 }
leafnodes { remotes: [ { urls: ["nats-leaf://127.0.0.1:$LEAFP"], nkey: \$LEAF_NKEY } ] }
CONF
  LEAF_NKEY="$(grep -m1 '^SU' "$3")" nats-server -c "$work/$1.conf" -l "$work/$1.log" &
  pids+=($!)
}
start_leaf l1 "$L1P" "$work/leaf-kinu.nk"
start_leaf l2 "$L2P" "$work/leaf-mokuzai.nk"
sleep 2
for l in l1 l2; do
  grep -q "JetStream using domains: local \"verify$l\", remote \"global\"" "$work/$l.log" \
    && ok "leaf $l linked to the hub" || { bad "leaf $l did not link" "$(tail -3 "$work/$l.log")"; }
done
LEAF1=(nats -s "nats://127.0.0.1:$L1P" --js-domain global --timeout 3s)
LEAF2=(nats -s "nats://127.0.0.1:$L2P" --js-domain global --timeout 3s)
"${LEAF1[@]}" stream info GLOBAL_TO_DIRECTOR >/dev/null 2>&1 && ok "the kinu-like leaf reads its own stream" || bad "kinu leaf cannot read its own stream"
"${LEAF1[@]}" stream info GLOBAL_TO_mokuzai >/dev/null 2>&1 && bad "the kinu-like leaf read another cluster's stream" || ok "the kinu-like leaf cannot read another cluster's stream"
"${LEAF1[@]}" stream ls -n >/dev/null 2>&1 && bad "the kinu-like leaf listed streams" || ok "the kinu-like leaf cannot list streams"
"${LEAF2[@]}" stream info GLOBAL_TO_mokuzai >/dev/null 2>&1 && ok "the mokuzai-like leaf reads its own stream" || bad "mokuzai leaf cannot read its own stream"
"${LEAF2[@]}" stream info GLOBAL_TO_kinu >/dev/null 2>&1 && bad "the mokuzai-like leaf read another cluster's stream" || ok "the mokuzai-like leaf cannot read another cluster's stream"

# --- shim: unread --global beside the mokuzai-like leaf ----------------------------
( cd "$SHIM_SRC" && go build -o "$work/director-mcp" . )
SHIM="$work/director-mcp"
shim_unread() { # extra env...
  env "NATS_URL=nats://127.0.0.1:$L2P" DIRECTOR_GLOBAL_DOMAIN=global DIRECTOR_CLUSTER=mokuzai "$@" "$SHIM" unread --global --json 2>"$work/shim.err"
}
out="$(shim_unread "DIRECTOR_HUB_OBSERVER_URL=$HUB" "DIRECTOR_HUB_OBSERVER_NKEY=$work/observer.nk" || true)"
streams="$(jq -r '[.reports[].stream] | sort | join(",")' <<<"$out" 2>/dev/null || true)"
[[ "$streams" == "GLOBAL_TO_DIRECTOR,GLOBAL_TO_kinu,GLOBAL_TO_mokuzai,GLOBAL_TO_probe,GLOBAL_TO_probe2" ]] \
  && ok "shim: with the observer, unread --global reads every cluster's stream" || bad "shim: observer read" "streams=[$streams] $(head -c 300 "$work/shim.err")"
[[ "$(jq -r '[.reports[].partial // false] | any' <<<"$out" 2>/dev/null || echo true)" == false ]] \
  && ok "shim: no report is marked partial" || bad "shim: a report is partial"
[[ "$(jq -r '[.reports[] | select(.stream=="GLOBAL_TO_kinu") | .durables[].pending] | add' <<<"$out" 2>/dev/null || true)" == 2 ]] \
  && ok "shim: another cluster's unread count is its real count (2), not 0" || bad "shim: kinu pending"
out="$(shim_unread || true)"
[[ "$(jq -r '[.reports[].stream] | join(",")' <<<"$out" 2>/dev/null || true)" == "GLOBAL_TO_mokuzai" && "$(jq -r '.reports[0].partial' <<<"$out" 2>/dev/null || true)" == true ]] \
  && ok "shim control: without the observer it reads its own cluster only, marked partial" || bad "shim control: no-grant path" "$(head -c 300 <<<"$out")"
out="$(env "NATS_URL=nats://127.0.0.1:$L2P" DIRECTOR_GLOBAL_DOMAIN=global DIRECTOR_CLUSTER=mokuzai "DIRECTOR_HUB_OBSERVER_URL=nats://127.0.0.1:$(port)" "DIRECTOR_HUB_OBSERVER_NKEY=$work/observer.nk" "$SHIM" unread --global 2>&1 || true)"
grep -q 'GAP observer' <<<"$out" && ok "shim: an unusable observer prints a GAP that names it" || bad "shim: no observer GAP" "$(head -c 300 <<<"$out")"
state >"$work/state.final"
cmp -s "$work/state.before" "$work/state.final" \
  && ok "state: the hub is unchanged after the shim reads too" || bad "state changed after the shim reads" "$(diff "$work/state.before" "$work/state.final" | head -8 | tr '\n' ';')"

echo "---"
echo "$pass passed, $fail failed"
[[ "$fail" == 0 ]]

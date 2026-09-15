#!/usr/bin/env bash
# Prove the shim's global mode (R-86) against a REAL hub, run from a cluster
# host that holds that cluster's leaf seed. The companion to verify-global.sh:
# that one proves the hub and its bindings with the nats CLI, this one proves
# what director-mcp does with them.
#
# Isolation: it starts one throwaway leaf broker of its own, with its own port,
# store and JetStream domain, so the live local broker and its live sessions
# are untouched (the same isolation verify-auth.sh and verify-global.sh keep).
# It does touch the shared hub, by design: the point is the real link.
#
# What it leaves there (director#38). The stand-in director presence row and its
# own durable are removed in cleanup; its own presence rows expire with the
# bucket TTL. The MESSAGES it stores are recorded as it goes and deleted by
# sequence in cleanup, never purged, because these are real inboxes and a purge
# to a sequence would take other principals' messages with it. A run can delete
# from the stream it consumes (this cluster's own GLOBAL_TO_<cluster>) and not
# from the one it publishes to (GLOBAL_TO_DIRECTOR), which is brief 8 section
# 4.1's asymmetry and is enforced at the hub by the credential. Whatever is left
# is printed with its stream and sequence at the end of the run rather than
# claimed clean: draining with a consumer does NOT remove a message, so residue
# on a DeliverAll inbox is replayed to the next real cast within the 24h max
# age.
#
# Usage, from anywhere:
#   probe/nats-global-tier/verify-global-shim.sh
# Knobs: DIRECTOR_CLUSTER (default mokuzai), DIRECTOR_LEAF_SEED,
# DIRECTOR_GLOBAL_DOMAIN (default global), HUB_LEAF, VERIFY_PORT.
#
# Exit nonzero on any miss.
set -euo pipefail

CLUSTER="${DIRECTOR_CLUSTER:-mokuzai}"
DOMAIN="${DIRECTOR_GLOBAL_DOMAIN:-global}"
SEED_FILE="${DIRECTOR_LEAF_SEED:-$HOME/.director/nats/leaf-$CLUSTER.nk}"
HUB_LEAF="${HUB_LEAF:-192.168.100.110:7442}"
PORT="${VERIFY_PORT:-4272}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SHIM_SRC="$here/../nats-phase-0/director-mcp"

command -v nats-server >/dev/null || { echo "nats-server not on PATH"; exit 2; }
command -v nats >/dev/null || { echo "nats CLI not on PATH"; exit 2; }
command -v jq >/dev/null || { echo "jq not on PATH"; exit 2; }
[[ -r "$SEED_FILE" ]] || { echo "leaf seed not readable: $SEED_FILE"; exit 2; }

work="$(mktemp -d)"
pids=()
STANDIN=""
DURABLE=""
RESIDUE_LEFT=0
LOCAL=(nats -s "nats://127.0.0.1:$PORT")
HUB=(nats -s "nats://127.0.0.1:$PORT" --js-domain "$DOMAIN")

# The hub-message ledger. Sourced before the trap is set, so cleanup can always
# call hub_drop. It reads HUB and DOMAIN from here.
# shellcheck source=hub-residue.sh
. "$here/hub-residue.sh"

cleanup() {
  # Messages first, and before the pids are killed: the throwaway leaf broker is
  # one of those pids and it is the route to the hub.
  hub_drop || true
  [[ -n "$STANDIN" ]] && "${HUB[@]}" kv del GLOBAL_PRESENCE "$STANDIN" -f >/dev/null 2>&1 || true
  [[ -n "$DURABLE" ]] && "${HUB[@]}" consumer rm "GLOBAL_TO_$CLUSTER" "$DURABLE" -f >/dev/null 2>&1 || true
  for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done
  rm -rf "$work"
}
trap cleanup EXIT

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

# --- the throwaway leaf -------------------------------------------------------
mkdir -p "$work/store"
cat > "$work/nats.conf" <<CONF
server_name: verify-shim-$CLUSTER
listen: 127.0.0.1:$PORT
jetstream { store_dir: "$work/store", domain: verifyshim$CLUSTER }
leafnodes { remotes: [ { urls: ["nats-leaf://$HUB_LEAF"], nkey: \$DIRECTOR_LEAF_NKEY } ] }
CONF
DIRECTOR_LEAF_NKEY="$(grep -m1 '^SU' "$SEED_FILE")" \
  nats-server -c "$work/nats.conf" -l "$work/broker.log" &
pids+=($!)
sleep 2
grep -q "JetStream using domains: local \"verifyshim$CLUSTER\", remote \"$DOMAIN\"" "$work/broker.log" \
  || { echo "leaf link did not come up; see $work/broker.log"; cat "$work/broker.log"; exit 2; }

# The local tier the shim needs, on the throwaway broker only.
"${LOCAL[@]}" stream add AGENT_INBOX --subjects 'agent.*.*.*.inbox,agent.*.*.role.*.inbox' \
  --storage file --retention limits --max-age 24h --max-msg-size 65536 --dupe-window 2m --defaults >/dev/null
"${LOCAL[@]}" stream add AGENT_AUDIT --subjects 'agent.audit' --storage file --retention limits \
  --max-age 30d --defaults >/dev/null
"${LOCAL[@]}" kv add AGENT_STATE --ttl 90s >/dev/null

# --- the shim -----------------------------------------------------------------
( cd "$SHIM_SRC" && go build -o "$work/director-mcp" . )
SHIM="$work/director-mcp"
AGENT="verifyshim-$RANDOM"
TEAM=fleet
WS=verifyws
run_shim_env=(
  "DIRECTOR_AGENT_ID=$AGENT" "DIRECTOR_TEAM=$TEAM" "DIRECTOR_WORKSPACE=$WS"
  "NATS_URL=nats://127.0.0.1:$PORT"
  "DIRECTOR_GLOBAL_DOMAIN=$DOMAIN" "DIRECTOR_CLUSTER=$CLUSTER" "DIRECTOR_GLOBAL_ROLE=supervisor"
)

# 1. A role that is not one of the two global words is refused at start, before
#    anything connects (R-94).
if env "${run_shim_env[@]}" DIRECTOR_GLOBAL_ROLE=worker "$SHIM" --preflight </dev/null >/dev/null 2>"$work/role.err"; then
  bad "a worker role started" "it should be refused at spawn"
else
  grep -q "supervisor" "$work/role.err" && grep -q "director" "$work/role.err" \
    && ok "a non-global role is refused at start, naming both legal roles" \
    || bad "role refusal message" "$(cat "$work/role.err")"
fi

# 2. Preflight reaches the hub through the domain and finds stream and bucket.
env "${run_shim_env[@]}" "$SHIM" --preflight </dev/null >/dev/null 2>"$work/pf.err" \
  && ok "preflight verifies the hub stream and bucket through domain $DOMAIN" \
  || bad "preflight against the real hub" "$(cat "$work/pf.err")"

# 3. Preflight fails loud for a cluster the hub has no stream for: an
#    unprovisioned or mis-cast supervisor must crash its pane, not come up
#    looking healthy and unreachable (finding-166 at the global tier).
if env "${run_shim_env[@]}" DIRECTOR_CLUSTER=nosuchcluster "$SHIM" --preflight </dev/null >/dev/null 2>"$work/pf2.err"; then
  bad "preflight passed for an unprovisioned cluster"
else
  grep -q "GLOBAL_TO_nosuchcluster" "$work/pf2.err" \
    && ok "preflight refuses an unprovisioned cluster and names the missing stream" \
    || bad "preflight refusal message" "$(cat "$work/pf2.err")"
fi

# 4. With the global tier off, preflight is the local check it always was.
env "DIRECTOR_AGENT_ID=$AGENT" "DIRECTOR_TEAM=$TEAM" "DIRECTOR_WORKSPACE=$WS" \
    "NATS_URL=nats://127.0.0.1:$PORT" "$SHIM" --preflight </dev/null >/dev/null 2>&1 \
  && ok "preflight with the global tier off is unchanged" \
  || bad "preflight with global off"

# --- drive the shim over its own stdio ----------------------------------------
mkfifo "$work/in" "$work/out"
start_shim() { # extra env...
  env "$@" "$SHIM" <"$work/in" >"$work/out" 2>"$work/shim.err" &
  pids+=($!)
  exec 3>"$work/in" 4<"$work/out"
  rpc 1 initialize '{}' >/dev/null
  notify notifications/initialized '{}'
}
stop_shim() { exec 3>&- 4<&- || true; }
notify() { printf '{"jsonrpc":"2.0","method":"%s","params":%s}\n' "$1" "$2" >&3; }
rpc() { # id method params -> the response line
  printf '{"jsonrpc":"2.0","id":%s,"method":"%s","params":%s}\n' "$1" "$2" "$3" >&3
  local line
  read -r -t "${RPC_TIMEOUT:-30}" -u 4 line || { echo '{"error":"rpc timeout"}'; return 0; }
  printf '%s\n' "$line"
}
call() { # id tool args-json -> the tool result text (or the isError text)
  rpc "$1" tools/call "$(jq -nc --arg n "$2" --argjson a "$3" '{name:$n,arguments:$a}')"
}
text_of() { jq -r '.result.content[0].text // "NO RESULT"'; }
iserr_of() { jq -r '.result.isError // false'; }
# A successful global send returns the hub stream and sequence it was stored at.
# Record them so cleanup accounts for them, even though a cluster credential
# cannot delete from GLOBAL_TO_DIRECTOR: an unremovable message that is named
# and disclosed is the honest outcome, an unremovable message nobody mentions
# is what director#38 found.
record_tool_send() { # tool-result-text what
  local st sq
  st="$(jq -r '.stream // empty' <<<"$1" 2>/dev/null)" || return 0
  sq="$(jq -r '.sequence // empty' <<<"$1" 2>/dev/null)" || return 0
  [[ -n "$st" && -n "$sq" ]] && hub_record "$st" "$sq" "$2"
  return 0
}

start_shim "${run_shim_env[@]}"
sleep 1
INSTANCE="$(sed -n 's/.*instance \([0-9A-Z]*\) .*/\1/p' "$work/shim.err" | head -1)"
DURABLE="mcp_global_${AGENT}_${INSTANCE}"

# 5. The catalog advertises this session's own global address, so a model that
#    receives a global message knows how it can be answered.
cat_out="$(rpc 2 tools/list '{}')"
jq -e --arg a "global://$CLUSTER/supervisor" \
  '.result.tools[] | select(.name=="send_message") | select(.description | contains($a))' \
  <<<"$cat_out" >/dev/null \
  && ok "send_message advertises this session as global://$CLUSTER/supervisor" \
  || bad "catalog global address"

# 6. Presence: the 30s ticker's row is in the hub bucket, carrying both tiers'
#    identity, so a global address resolves back to a local session (R-06, R-79).
prec="$("${HUB[@]}" kv get GLOBAL_PRESENCE "presence.$CLUSTER.supervisor.$INSTANCE" --raw 2>/dev/null || true)"
if [[ -n "$prec" ]] && jq -e --arg a "$AGENT" --arg c "$CLUSTER" \
     'select(.agent_id==$a and .cluster==$c and .role=="supervisor" and .workspace!="" and .team!="" and .instance!="" and .pid!=null and .state!="" and .ts!="")' \
     <<<"$prec" >/dev/null; then
  ok "GLOBAL_PRESENCE carries cluster, role, agent_id, workspace, team, instance, pid, state and ts"
else
  bad "global presence record" "${prec:-absent}"
fi

# 7. The roster merges both tiers with a tier column.
roster="$(call 3 list_roster '{}' | text_of)"
lt="$(jq '[.present[] | select(.tier=="local")] | length' <<<"$roster")"
gt="$(jq '[.present[] | select(.tier=="global")] | length' <<<"$roster")"
[[ "$lt" -ge 1 && "$gt" -ge 1 ]] \
  && ok "list_roster merges local ($lt) and global ($gt) rows with a tier column" \
  || bad "roster merge" "local=$lt global=$gt"

# 8. R-92 liveness: a global address with no live presence refuses BEFORE
#    publish, and nothing is written, not even the audit mirror.
audit_before="$("${LOCAL[@]}" stream info AGENT_AUDIT -j | jq .state.messages)"
resp="$(call 4 send_message '{"to":"global://nosuchcluster/supervisor","performative":"INFORM","text":"nobody home"}')"
audit_after="$("${LOCAL[@]}" stream info AGENT_AUDIT -j | jq .state.messages)"
if [[ "$(iserr_of <<<"$resp")" == "true" ]] && [[ "$(text_of <<<"$resp")" == *R-92* ]] && [[ "$audit_before" == "$audit_after" ]]; then
  ok "a global send with zero live presence refuses (R-92) and stores nothing"
else
  bad "R-92 liveness refusal" "$(text_of <<<"$resp") [audit $audit_before -> $audit_after]"
fi

# 9. The address grammar holds: there is one director seat, not one per cluster.
resp="$(call 5 send_message "$(jq -nc --arg t "global://$CLUSTER/director" '{to:$t,performative:"INFORM",text:"x"}')")"
[[ "$(iserr_of <<<"$resp")" == "true" ]] \
  && ok "global://<cluster>/director is refused; the director is one seat (R-94)" \
  || bad "per-cluster director accepted"

# 10. The real cross-host leg: a send to the director lands in the hub's own
#     stream on the hub host. A stand-in presence row stands for the director
#     seat until its shim runs global mode too.
STANDIN="presence.director.VERIFYSHIM$RANDOM"
"${HUB[@]}" kv put GLOBAL_PRESENCE "$STANDIN" \
  "$(jq -nc --arg i "$STANDIN" '{cluster:"kinu",role:"director",agent_id:"verify-standin",instance:$i,state:"idle"}')" >/dev/null
resp="$(call 6 send_message '{"to":"global://director","performative":"INFORM","text":"verify-global-shim.sh self test, not real traffic"}')"
out="$(text_of <<<"$resp")"
record_tool_send "$out" "outbound leg, test 10"
if [[ "$(iserr_of <<<"$resp")" != "true" ]] && \
   jq -e 'select(.tier=="global" and .stream=="GLOBAL_TO_DIRECTOR" and .sequence>0)' <<<"$out" >/dev/null; then
  ok "a send to global://director is stored in GLOBAL_TO_DIRECTOR on the hub (seq $(jq -r .sequence <<<"$out"))"
else
  bad "global send to the director" "$out"
fi

# 11. Inbound: the director's leg of the channel, published into this cluster's
#     hub stream, is delivered to the shim and marked as the global tier.
# The envelope is recorded so cleanup can delete it by sequence, and it is
# built to be harmless if a delete is ever refused and it survives: INFORM
# rather than REQUEST, from this run's own workspace rather than a real one,
# and sender "verify-global-shim" rather than a "verify-director" principal
# that does not exist. A replayed REQUEST on a DeliverAll inbox is the case
# director#38 was filed about.
NONCE="verify-$RANDOM"
hub_publish "global.$CLUSTER.supervisor.inbox" "$(jq -nc --arg n "$NONCE" --arg t "$CLUSTER" --arg w "$WS" '{
  schema_version:1, message_id:$n, conversation_id:("cid-"+$n),
  sender:{agent_id:"verify-global-shim", workspace:$w, principal:null},
  recipient:{address:("global://"+$t+"/supervisor")},
  performative:"INFORM",
  content:{type:"text", data:"verify-global-shim.sh self test, not director traffic; safe to ignore"},
  sent_at:"2026-09-15T00:00:00Z", trace:{otel_traceparent:null}}')" \
  "inbound envelope, test 11" || true
got=""
for _ in 1 2 3 4 5 6; do
  out="$(RPC_TIMEOUT=40 call 7 wait_for_message '{"timeout_seconds":20}' | text_of)"
  if [[ "$(jq -r '.message.message_id // ""' <<<"$out")" == "$NONCE" ]]; then got="$out"; break; fi
done
if [[ -n "$got" ]] && [[ "$(jq -r .tier <<<"$got")" == "global" ]]; then
  ok "the director's message arrives on the global inbox, marked tier=global"
else
  bad "inbound global delivery" "${got:-not received}"
fi

# 12. The receipt across the link (design brief 8 section 8's open item): the
#     reply carries in_reply_to, correlates, and is stored in the hub's
#     director stream. The local audit mirror is what lets this side read back
#     what it sent, since a cluster credential cannot consume that stream.
resp="$(call 8 send_message "$(jq -nc --arg n "$NONCE" '{to:"global://director",performative:"AGREE",text:"verify-global-shim.sh self test receipt",in_reply_to:$n}')")"
out="$(text_of <<<"$resp")"
record_tool_send "$out" "receipt, test 12"
reply_id="$(jq -r '.message_id // ""' <<<"$out")"
mirrored="$("${LOCAL[@]}" stream get AGENT_AUDIT --last-for agent.audit -j 2>/dev/null | jq -r '.data' | base64 -d 2>/dev/null || true)"
if jq -e 'select(.stream=="GLOBAL_TO_DIRECTOR" and .tier=="global")' <<<"$out" >/dev/null \
   && [[ -n "$reply_id" ]] \
   && jq -e --arg n "$NONCE" --arg r "$reply_id" \
        'select(.message_id==$r and .in_reply_to==$n and .correlation_id==$n and .recipient.address=="global://director" and (.recipient.team // "")=="")' \
        <<<"$mirrored" >/dev/null; then
  ok "the receipt crosses the link with in_reply_to and correlation_id set, team empty"
else
  bad "receipt correlation across the link" "$out / $mirrored"
fi

# 13. The local tier is untouched by any of this, and says which tier it is.
resp="$(call 9 send_message "$(jq -nc --arg t "$TEAM" --arg a "$AGENT" '{to:("agent://"+$t+"/"+$a),performative:"INFORM",text:"to myself"}')")"
self_id="$(text_of <<<"$resp" | jq -r .message_id)"
got=""
for _ in 1 2 3 4 5 6; do
  out="$(RPC_TIMEOUT=40 call 10 wait_for_message '{"timeout_seconds":20}' | text_of)"
  if [[ "$(jq -r '.message.message_id // ""' <<<"$out")" == "$self_id" ]]; then got="$out"; break; fi
done
[[ -n "$got" && "$(jq -r .tier <<<"$got")" == "local" ]] \
  && ok "a local message still arrives, marked tier=local" \
  || bad "local delivery with a tier marker" "${got:-not received}"
stop_shim

# 14. With the tier off, a global address is refused with a reason, not routed.
rm -f "$work/in" "$work/out"; mkfifo "$work/in" "$work/out"
start_shim "DIRECTOR_AGENT_ID=$AGENT-off" "DIRECTOR_TEAM=$TEAM" "DIRECTOR_WORKSPACE=$WS" \
           "NATS_URL=nats://127.0.0.1:$PORT"
resp="$(call 11 send_message '{"to":"global://director","performative":"INFORM","text":"x"}')"
[[ "$(iserr_of <<<"$resp")" == "true" ]] && [[ "$(text_of <<<"$resp")" == *DIRECTOR_GLOBAL_DOMAIN* ]] \
  && ok "with the global tier off a global address is refused, naming the lever" \
  || bad "global-off refusal" "$(text_of <<<"$resp")"
stop_shim

# cleanup runs on EXIT, after this line, so the residue count it prints appears
# below the summary. Say so rather than printing a number that is not final yet.
echo "$pass passed, $fail failed"
echo "hub messages recorded this run: ${#residue_stream[@]}; cleanup reports below what it could not remove"
[[ $fail -eq 0 ]]

#!/usr/bin/env bash
# R-86 round trip with receipts, two real shims, two credentials (aae-orc-2vwae).
#
# WHAT THIS PROVES THAT verify-global-shim.sh DOES NOT. That script drives ONE
# shim and simulates the far side in the three places that decide the answer: a
# stand-in director presence row it writes itself, an inbound envelope it
# hand-publishes with the nats CLI, and a "receipt" at its test 12 that it reads
# back out of its OWN local audit mirror. aae-orc-2vwae names that last shape as
# a fail condition in advance, "a receipt that is the sender's own write rather
# than the recipient's". So this script runs TWO real shims, on two leaves, with
# two separate credentials, and the only thing it will count as an answer is a
# message the OTHER side wrote.
#
# R-08 IS THE LOAD-BEARING RULE. "Accepted for delivery" is never the answer.
# The send acknowledgement is recorded and then explicitly not counted, and
# check 7 is a negative self-test that FAILS THE RUN if this harness could ever
# report success on an acknowledgement alone. A harness that cannot fail that
# way is the whole reason this file exists.
#
# ISOLATION. Everything is throwaway: its own hub, its own two leaves, its own
# nkeys, its own ports, its own JetStream stores, all under one mktemp dir.
# Nothing touches the live broker, the live hub, or any live session, so the
# round trip is deterministic and safe to gate a PR on. The subject grammar,
# the cluster names and the credential asymmetry are copied from the production
# hub config on purpose: a test that grants more than production grants would
# pass where production fails.
#
# WHAT ISOLATED MODE DOES NOT PROVE, stated because a check that overclaims is
# worse than one that is not run: it does not exercise the real WAN hop, the
# real hub, or the real leaf seeds. av2v1's leg 1 already proved that transport.
#
# WHY THE LIVE LEG IS NOT OURS TO RUN. This stands on its own because it is a
# PERMISSION fact, not a caution, and reading it as a caution invites someone
# to decide the risk is acceptable and run it anyway. In marvel's rendered
# authorization.conf every team user's global publish allow is exactly
# global.director.inbox, so NO session on a cluster can publish into a cluster
# inbox at all. The live attestation therefore has to be INITIATED BY THE
# DIRECTOR SEAT: it is not a thing this rig is declining to do, it is a thing
# no session here can do. It is the second layer of aae-orc-2vwae and needs
# operator clearance. Check 11 reports it as NOT RUN so a reader of the output
# learns it too, rather than only a reader of this header.
#
# FAN-OUT, measured on mokuzai 2026-09-21, and the reason this defaults to
# isolated. Every supervisor session builds a per-session durable on
# GLOBAL_TO_<cluster> with the SAME FilterSubject global.<cluster>.supervisor.inbox
# (global.go ensureConsumer) under DeliverAllPolicy, and the shim's fetchOne
# applies no recipient filter. So one REQUEST to global://<cluster>/supervisor is
# delivered to EVERY live supervisor on that cluster, and replayed in full to
# every supervisor session started inside the stream's 24h max age. Publishing a
# test REQUEST at a live cluster is therefore not a private act. That is the
# director#38 shape, and it is a reason to be careful rather than the reason
# the live leg is unavailable, which is the permission fact above. It is why
# the deterministic gate builds its own hub
# instead of borrowing the fleet's.
#
# WHERE THE OBSERVATION PATH DIVERGES FROM THE PATH UNDER TEST, and where it
# cannot. If a harness and the thing it measures share a code path, one bug makes
# both agree and the agreement reads as corroboration, so this is stated rather
# than left for a reviewer to reconstruct.
#
# Independent of the shim entirely, observed with the nats CLI or the hub admin
# nkey, which is a third principal that writes nothing:
#   - the receipt's RAW BYTES on the hub, fetched by sequence as the admin and
#     parsed here, so in_reply_to and the digest are read without either shim's
#     decode in the path (check 6b);
#   - presence on both tiers, read straight from the KV buckets rather than
#     through the shim's own list_roster (checks 0, 1);
#   - the enqueue, read as num_pending on the recipient's durable through the
#     raw JetStream API (check 4a);
#   - the credential asymmetry (check 9).
#
# NOT independent: both sides run the same director-mcp binary, so envelope
# encode/decode and durable construction are one implementation used twice.
#
# The residual is ONE IDENTIFIER, not three fields, and the provenance of each
# expected value is what decides it. The digest is a sha computed here in the
# shell BEFORE the send, so it is harness-sourced and a symmetric encode/decode
# bug cannot hide from it. Authorship is compared against the agent id this
# script set in the shim's environment, so it is harness-sourced too. Only
# in_reply_to is compared against REQ_ID, which comes from the shim's OWN send
# acknowledgement, so a self-consistent but wrong id scheme would satisfy it.
# That single identifier is the uncovered thing. Understating this has a cost of
# its own: a reader who takes "the observation is not independent" literally
# distrusts all three fields when only one is affected.
#
# And 6b catches a class it is not obvious it catches: because the shell parses
# the RAW WIRE BYTES with jq, any symmetric bug in field NAMING or NESTING is
# caught, since the shell reads the contract's spelling rather than whatever the
# shim would have called it on the way back in.
#
# NEGATIVE-CONTROL COVERAGE, honestly bounded. selftest-roundtrip-receipts.sh
# drives red runs for the receipt assertions (in_reply_to, authorship, digest,
# performative), the no-receipt case, the drain case, the transport case and the
# credential asymmetry. It does NOT have a red run for the no-rename check, the
# catalog check or the instance-collision check, because injecting those needs a
# patched shim and this ticket is instrument-only. Those three are asserted but
# not negatively controlled, and that is a real limit of this harness.
#
# Usage:
#   probe/nats-global-tier/verify-roundtrip-receipts.sh
# Knobs: CLUSTER_A (director side, default kinu), CLUSTER_B (supervisor side,
# default mokuzai), DOMAIN (default global), and the five ports below.
#
# Exit nonzero on any miss. NOT RUN is never PASS (finding-157).
set -euo pipefail

HUB_CLIENT_PORT="${HUB_CLIENT_PORT:-14242}"
HUB_LEAF_PORT="${HUB_LEAF_PORT:-17442}"
HUB_HTTP_PORT="${HUB_HTTP_PORT:-18242}"
LEAF_A_PORT="${LEAF_A_PORT:-14262}"
LEAF_B_PORT="${LEAF_B_PORT:-14263}"
CLUSTER_A="${CLUSTER_A:-kinu}"
CLUSTER_B="${CLUSTER_B:-mokuzai}"
DOMAIN="${DOMAIN:-global}"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SHIM_SRC="$here/../nats-phase-0/director-mcp"

for t in nats-server nats jq go; do
  command -v "$t" >/dev/null || { echo "$t not on PATH"; exit 2; }
done
if command -v shasum >/dev/null; then sha() { printf '%s' "$1" | shasum -a 256 | awk '{print $1}'; }
elif command -v sha256sum >/dev/null; then sha() { printf '%s' "$1" | sha256sum | awk '{print $1}'; }
else echo "no sha256 tool on PATH"; exit 2; fi

work="$(mktemp -d)"
pids=()        # brokers: the hub and the two leaves
shim_pids=()   # the two shims, killed FIRST so BEAT-C can reach a live hub
cleanup() {
  for p in "${shim_pids[@]:-}"; do kill "$p" 2>/dev/null || true; done
  sleep 0.3
  for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
  if [[ -n "${KEEP:-}" ]]; then echo "work dir kept at $work"; else rm -rf "$work"; fi
}
trap cleanup EXIT

# Wall clock in ms, for the enqueued/dequeued split below.
now_ms() { local e="${EPOCHREALTIME/./}"; echo $(( e / 1000 )); }

pass=0; fail=0; notrun=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
nr()  { echo "NOT RUN $1${2:+ -- $2}"; notrun=$((notrun+1)); }

# --- keys ---------------------------------------------------------------
# One nkey per principal, exactly as the hub ceremony does it. Seeds live in
# the throwaway dir for the life of the run and are removed with it.
mkdir -p "$work/keys"; chmod 700 "$work/keys"
for k in admin "leaf-$CLUSTER_A" "leaf-$CLUSTER_B"; do
  nats auth nkey gen user > "$work/keys/$k.nk" 2>/dev/null
  chmod 600 "$work/keys/$k.nk"
done
pub() { nats auth nkey show "$work/keys/$1.nk"; }

# --- the throwaway hub --------------------------------------------------
# The two leaf grant blocks are the production shapes from hub/nats-server.conf,
# including the asymmetry that decides this test: a cluster leaf may PUBLISH to
# global.director.inbox but may not read or delete GLOBAL_TO_DIRECTOR, and may
# consume and delete only from its own GLOBAL_TO_<cluster>. The director leaf is
# the mirror image.
#
# Do not widen these to make a check pass. A rig that grants more than
# production grants passes in exactly the case where production fails, which is
# the one case anybody is running this for. If a future hub config diverges from
# these blocks, copy the new production shape here rather than relaxing them;
# a harness whose permissions are more generous than the system under test is
# measuring a system that does not exist.
#
# Check 9 exists for the same reason and is not decoration. It asserts the
# asymmetry still holds at run time, because if a cluster credential ever COULD
# read GLOBAL_TO_DIRECTOR, the receipt this script trusts could have been read
# back by the sender rather than received from the recipient, and the whole
# instrument would quietly decay into the audit-mirror shape it was written to
# replace. selftest-roundtrip-receipts.sh drives FAULT=grant_asymmetry_broken
# specifically so that check has its own red run and cannot rot unnoticed.
leaf_grants() { # cluster pub-extra
  local c="$1"
  cat <<CONF
        permissions {
          publish   { allow: [ "global.director.inbox", "global.$c.>",
                               "\$JS.$DOMAIN.API.CONSUMER.CREATE.GLOBAL_TO_$c.>",
                               "\$JS.$DOMAIN.API.CONSUMER.DURABLE.CREATE.GLOBAL_TO_$c.>",
                               "\$JS.$DOMAIN.API.CONSUMER.INFO.GLOBAL_TO_$c.>",
                               "\$JS.$DOMAIN.API.CONSUMER.MSG.NEXT.GLOBAL_TO_$c.>",
                               "\$JS.$DOMAIN.API.CONSUMER.DELETE.GLOBAL_TO_$c.>",
                               "\$JS.$DOMAIN.API.STREAM.INFO.GLOBAL_TO_$c",
                               "\$JS.$DOMAIN.API.STREAM.MSG.DELETE.GLOBAL_TO_$c",
                               "\$JS.$DOMAIN.API.INFO",
                               "\$JS.$DOMAIN.API.\$KV.GLOBAL_PRESENCE.>",
                               "\$JS.$DOMAIN.API.STREAM.INFO.KV_GLOBAL_PRESENCE",
                               "\$JS.$DOMAIN.API.DIRECT.GET.KV_GLOBAL_PRESENCE.>",
                               "\$JS.$DOMAIN.API.STREAM.MSG.GET.KV_GLOBAL_PRESENCE",
                               "\$JS.$DOMAIN.API.CONSUMER.CREATE.KV_GLOBAL_PRESENCE.>",
                               "\$JS.$DOMAIN.API.CONSUMER.INFO.KV_GLOBAL_PRESENCE.>",
                               "\$JS.$DOMAIN.API.CONSUMER.DELETE.KV_GLOBAL_PRESENCE.>",
                               "\$JS.ACK.>"${EXTRA_PUB:-} ] }
          subscribe { allow: [ "global.$c.>", "_INBOX.>"${EXTRA_SUB:-} ] }
        }
CONF
}
# The director's leaf reads the fleet inbox instead of a cluster's.
director_leaf_grants() {
  cat <<CONF
        permissions {
          publish   { allow: [ "global.*.supervisor.inbox", "global.director.>",
                               "\$JS.$DOMAIN.API.CONSUMER.CREATE.GLOBAL_TO_DIRECTOR.>",
                               "\$JS.$DOMAIN.API.CONSUMER.DURABLE.CREATE.GLOBAL_TO_DIRECTOR.>",
                               "\$JS.$DOMAIN.API.CONSUMER.INFO.GLOBAL_TO_DIRECTOR.>",
                               "\$JS.$DOMAIN.API.CONSUMER.MSG.NEXT.GLOBAL_TO_DIRECTOR.>",
                               "\$JS.$DOMAIN.API.CONSUMER.DELETE.GLOBAL_TO_DIRECTOR.>",
                               "\$JS.$DOMAIN.API.STREAM.INFO.GLOBAL_TO_DIRECTOR",
                               "\$JS.$DOMAIN.API.STREAM.MSG.DELETE.GLOBAL_TO_DIRECTOR",
                               "\$JS.$DOMAIN.API.INFO",
                               "\$JS.$DOMAIN.API.\$KV.GLOBAL_PRESENCE.>",
                               "\$JS.$DOMAIN.API.STREAM.INFO.KV_GLOBAL_PRESENCE",
                               "\$JS.$DOMAIN.API.DIRECT.GET.KV_GLOBAL_PRESENCE.>",
                               "\$JS.$DOMAIN.API.STREAM.MSG.GET.KV_GLOBAL_PRESENCE",
                               "\$JS.$DOMAIN.API.CONSUMER.CREATE.KV_GLOBAL_PRESENCE.>",
                               "\$JS.$DOMAIN.API.CONSUMER.INFO.KV_GLOBAL_PRESENCE.>",
                               "\$JS.$DOMAIN.API.CONSUMER.DELETE.KV_GLOBAL_PRESENCE.>",
                               "\$JS.ACK.>" ] }
          subscribe { allow: [ "global.director.>", "_INBOX.>" ] }
        }
CONF
}

# FAULT=grant_asymmetry_broken widens the cluster leaf so it CAN read the
# director stream. Check 9 must fail, which is the negative control proving that
# check is load-bearing rather than decorative: if the asymmetry ever broke for
# real, the receipt could be read instead of received.
EXTRA_SUB=""
EXTRA_PUB=""
# FAULT=grant_asymmetry_broken_leaf_dead is the MIS-PROVOCATION. It sets the
# same grant up, then kills the leaf just before check 9 so the check fails at
# its POSITIVE CONTROL instead of at the assertion the fault aims for. It exists
# to be NOT caught: the red driver must report it MISSED, because a matcher that
# still says CAUGHT here is matching the label rather than the branch. See the
# note above the faults array in selftest-roundtrip-receipts.sh.
if [[ "${FAULT:-}" == "grant_asymmetry_broken" || "${FAULT:-}" == "grant_asymmetry_broken_leaf_dead" ]]; then
  EXTRA_SUB=', "global.director.>"'
  EXTRA_PUB=', "$JS.'"$DOMAIN"'.API.STREAM.INFO.GLOBAL_TO_DIRECTOR"'
  echo "FAULT grant_asymmetry_broken: the $CLUSTER_B leaf is being granted read on the director stream"
fi

mkdir -p "$work/hub/store"
{
  echo "server_name: verify-hub"
  echo "listen: 127.0.0.1:$HUB_CLIENT_PORT"
  echo "http: 127.0.0.1:$HUB_HTTP_PORT"
  echo "jetstream { store_dir: \"$work/hub/store\", domain: $DOMAIN }"
  echo "leafnodes { listen: 127.0.0.1:$HUB_LEAF_PORT }"
  echo "accounts {"
  echo "  FLEET {"
  echo "    jetstream: enabled"
  echo "    users: ["
  echo "      { nkey: $(pub admin) }"
  echo "      { nkey: $(pub "leaf-$CLUSTER_A")"
  director_leaf_grants
  echo "      }"
  echo "      { nkey: $(pub "leaf-$CLUSTER_B")"
  leaf_grants "$CLUSTER_B"
  echo "      }"
  echo "    ]"
  echo "  }"
  echo "}"
} > "$work/hub/nats.conf"

nats-server -c "$work/hub/nats.conf" -l "$work/hub/log" &
pids+=($!)

A=(nats -s "nats://127.0.0.1:$HUB_CLIENT_PORT" --nkey "$work/keys/admin.nk")
for _ in $(seq 1 40); do "${A[@]}" server check connection >/dev/null 2>&1 && break; sleep 0.25; done
"${A[@]}" server check connection >/dev/null 2>&1 \
  || { echo "hub did not come up"; sed -n '1,40p' "$work/hub/log"; exit 2; }

# --- provision the hub (hub/provision.sh's shape, idempotent) -----------
add_stream() { # name subject
  "${A[@]}" stream add "$1" --subjects "$2" --storage file --retention limits \
    --max-age 24h --max-msg-size 65536 --dupe-window 2m --defaults >/dev/null
}
add_stream GLOBAL_TO_DIRECTOR 'global.director.>'
add_stream "GLOBAL_TO_$CLUSTER_A" "global.$CLUSTER_A.>"
add_stream "GLOBAL_TO_$CLUSTER_B" "global.$CLUSTER_B.>"
"${A[@]}" kv add GLOBAL_PRESENCE --ttl 90s --storage file >/dev/null

# --- the two leaves -----------------------------------------------------
start_leaf() { # name port keyfile
  mkdir -p "$work/$1/store"
  cat > "$work/$1/nats.conf" <<CONF
server_name: verify-leaf-$1
listen: 127.0.0.1:$2
jetstream { store_dir: "$work/$1/store", domain: verify$1 }
leafnodes { remotes: [ { urls: ["nats-leaf://127.0.0.1:$HUB_LEAF_PORT"], nkey: \$LEAF_NKEY } ] }
CONF
  LEAF_NKEY="$(grep -m1 '^S' "$3")" nats-server -c "$work/$1/nats.conf" -l "$work/$1/log" &
  pids+=($!)
}
start_leaf "$CLUSTER_A" "$LEAF_A_PORT" "$work/keys/leaf-$CLUSTER_A.nk"
LEAF_A_PID=$!
start_leaf "$CLUSTER_B" "$LEAF_B_PORT" "$work/keys/leaf-$CLUSTER_B.nk"
LEAF_B_PID=$!

for c in "$CLUSTER_A" "$CLUSTER_B"; do
  up=no
  for _ in $(seq 1 60); do
    [[ -s "$work/$c/log" ]] && grep -q "JetStream using domains: local \"verify$c\", remote \"$DOMAIN\"" "$work/$c/log" && { up=yes; break; }
    sleep 0.25
  done
  [[ "$up" == yes ]] || { echo "leaf $c did not link"; sed -n '1,40p' "$work/$c/log"; exit 2; }
done

# The local tier each shim needs, on its own leaf only.
for p in "$LEAF_A_PORT" "$LEAF_B_PORT"; do
  L=(nats -s "nats://127.0.0.1:$p")
  "${L[@]}" stream add AGENT_INBOX --subjects 'agent.*.*.*.inbox,agent.*.*.role.*.inbox' \
    --storage file --retention limits --max-age 24h --max-msg-size 65536 --dupe-window 2m --defaults >/dev/null
  "${L[@]}" stream add AGENT_AUDIT --subjects 'agent.audit' --storage file --retention limits \
    --max-age 30d --defaults >/dev/null
  "${L[@]}" kv add AGENT_STATE --ttl 90s >/dev/null
done

# --- the two shims ------------------------------------------------------
( cd "$SHIM_SRC" && go build -o "$work/director-mcp" . )
SHIM="$work/director-mcp"
# Page the binary in before either real start, and keep this even though the
# stagger below looks like it should be enough on its own. It is not. The FIRST
# exec of a freshly built binary is cold and can take longer to reach package
# init than the stagger, so the second process (now hot, because the first
# paged the binary in) catches it and they initialise in the same tick anyway.
# Measured here 2026-09-21 with the stagger already correct: 0 collisions in 10
# runs with this warm-up, 1 in 4 without it. I removed it once on the reasoning
# that the fd barrier made it redundant, and the collision came straight back.
env DIRECTOR_AGENT_ID=warmup DIRECTOR_TEAM=t DIRECTOR_WORKSPACE=w \
    NATS_URL=nats://127.0.0.1:9 "$SHIM" --preflight </dev/null >/dev/null 2>&1 || true
AGENT_D="verify-director-g1-0"
AGENT_S="verify-supervisor-g1-0"

mkfifo "$work/d.in" "$work/d.out" "$work/s.in" "$work/s.out"
env "DIRECTOR_AGENT_ID=$AGENT_D" DIRECTOR_TEAM=ops DIRECTOR_WORKSPACE=verifyws \
    "NATS_URL=nats://127.0.0.1:$LEAF_A_PORT" \
    "DIRECTOR_GLOBAL_DOMAIN=$DOMAIN" "DIRECTOR_CLUSTER=$CLUSTER_A" DIRECTOR_GLOBAL_ROLE=director \
    "$SHIM" <"$work/d.in" >"$work/d.out" 2>"$work/d.err" &
shim_pids+=($!); D_PID=$!
# The two starts are staggered, and the stagger is applied to the fd opens
# below rather than to these launches, which is the whole point of this note.
#
# ulid.Make() seeds math/rand from time.Now().UnixNano() at package init
# (oklog/ulid/v2 v2.1.2, ulid.go:135-137), so two processes whose init lands in
# the same clock tick draw the same stream and, in the same millisecond, mint
# the SAME instance. Measured on this host 2026-09-21: 47 collisions in 200
# simultaneous pairs, 0 in 80 pairs whose starts were 300ms apart, and 0 in 80
# more where the starts were 300ms apart but the ulid.Make() calls were forced
# to land together. Separation of the INITS is what avoids it.
#
# A first draft put a sleep between the two launches below and believed that
# separated them. It did not, and the trap is worth keeping written down: a
# FIFO opened for reading blocks until someone opens the write end, so each
# backgrounded shell below parks on its `<"$work/*.in"` redirect and never
# reaches exec. Both processes were then released together by the single
# `exec 3> ... 5> ...` that used to open both write ends at once, so they
# initialised in the same tick and collided about one run in six. The sleep
# between the launches was decoration. The real start barrier is the fd open,
# so that is where the stagger has to go.
#
# Check 0 asserts the instances differ. It is not decoration either: it is what
# caught this, and it is the only reason the paragraph above says what actually
# happens instead of what I first assumed. The upstream defect it guards is
# director#62, which matters well beyond this harness because the global
# presence key is presence.<cluster>.<role>.<instance> and carries no agent id,
# so two same-cluster same-role sessions that collide collapse into one row.
env "DIRECTOR_AGENT_ID=$AGENT_S" DIRECTOR_TEAM=migrated DIRECTOR_WORKSPACE=verifyws \
    "NATS_URL=nats://127.0.0.1:$LEAF_B_PORT" \
    "DIRECTOR_GLOBAL_DOMAIN=$DOMAIN" "DIRECTOR_CLUSTER=$CLUSTER_B" DIRECTOR_GLOBAL_ROLE=supervisor \
    "$SHIM" <"$work/s.in" >"$work/s.out" 2>"$work/s.err" &
shim_pids+=($!); S_PID=$!
exec 3>"$work/d.in" 4<"$work/d.out"
sleep 0.3
exec 5>"$work/s.in" 6<"$work/s.out"

# Two stdio drivers, one per side, so no call can be served by the wrong shim.
# A timed-out RPC is recorded in a FILE, not a variable: every call site runs
# these in $( ), so a variable set here would die with the subshell and the
# timeout would read downstream as a clean empty. That conflation is the
# criterion-2 failure ("green by absence") arriving through the transport
# rather than through the assertion, and it is why the marker is a file.
rpc_timeout_clear() { rm -f "$work/rpc.timeout"; }
rpc_timed_out()     { [[ -e "$work/rpc.timeout" ]]; }
d_rpc() { printf '{"jsonrpc":"2.0","id":%s,"method":"%s","params":%s}\n' "$1" "$2" "$3" >&3
          local l; read -r -t "${RPC_TIMEOUT:-30}" -u 4 l || { : > "$work/rpc.timeout"; echo '{"error":"rpc timeout"}'; return 0; }; printf '%s\n' "$l"; }
s_rpc() { printf '{"jsonrpc":"2.0","id":%s,"method":"%s","params":%s}\n' "$1" "$2" "$3" >&5
          local l; read -r -t "${RPC_TIMEOUT:-30}" -u 6 l || { : > "$work/rpc.timeout"; echo '{"error":"rpc timeout"}'; return 0; }; printf '%s\n' "$l"; }
d_note() { printf '{"jsonrpc":"2.0","method":"%s","params":%s}\n' "$1" "$2" >&3; }
s_note() { printf '{"jsonrpc":"2.0","method":"%s","params":%s}\n' "$1" "$2" >&5; }
d_call() { d_rpc "$1" tools/call "$(jq -nc --arg n "$2" --argjson a "$3" '{name:$n,arguments:$a}')"; }
s_call() { s_rpc "$1" tools/call "$(jq -nc --arg n "$2" --argjson a "$3" '{name:$n,arguments:$a}')"; }
text_of()  { jq -r '.result.content[0].text // "NO RESULT"'; }
iserr_of() { jq -r '.result.isError // false'; }

d_rpc 1 initialize '{}' >/dev/null; d_note notifications/initialized '{}'
s_rpc 1 initialize '{}' >/dev/null; s_note notifications/initialized '{}'
# Poll for each instance rather than sleeping a fixed second: a shim that has
# not yet flushed its banner scrapes as empty, and on the first draft of this
# script that race silently reported one side's instance for both.
read_instance() { sed -n 's/.*instance \([0-9A-Z]*\) .*/\1/p' "$1" | head -1; }
INST_D=""; INST_S=""
for _ in $(seq 1 60); do
  [[ -n "$INST_D" ]] || INST_D="$(read_instance "$work/d.err")"
  [[ -n "$INST_S" ]] || INST_S="$(read_instance "$work/s.err")"
  [[ -n "$INST_D" && -n "$INST_S" ]] && break
  sleep 0.25
done
[[ -n "$INST_D" && -n "$INST_S" ]] || { echo "could not read both instances"; cat "$work/d.err" "$work/s.err"; exit 2; }

HUB_B=(nats -s "nats://127.0.0.1:$LEAF_B_PORT" --js-domain "$DOMAIN")
LOC_B=(nats -s "nats://127.0.0.1:$LEAF_B_PORT")

# The recipient's own durable on the hub stream. Reading num_pending on it is
# what separates the two mechanisms that both present to a sender as
# "accepted for delivery and never answered":
#   transport  the message is not in the stream, or not on a durable whose
#              filter matches, so it was never going to arrive;
#   drain      the message IS on the recipient's durable, correctly addressed,
#              and the recipient simply has not polled.
# A harness that reports one delivery event cannot tell these apart, and would
# blame the bus for an agent parked at a prompt.
DURABLE_S="mcp_global_${AGENT_S}_${INST_S}"
consumer_info() { # -> json, or empty
  nats -s "nats://127.0.0.1:$LEAF_B_PORT" req --timeout 5s --raw \
    "\$JS.$DOMAIN.API.CONSUMER.INFO.GLOBAL_TO_$CLUSTER_B.$DURABLE_S" '{}' 2>/dev/null
}

echo "# aae-orc-2vwae, isolated two-shim round trip"
# The cluster names are the production ones on purpose (the subject grammar and
# the credential asymmetry are copied from the live hub config), but nothing
# here runs on those hosts. Saying "on kinu" would be the same overclaim the
# rest of this output is written to avoid, so the line says what it is.
echo "# director $AGENT_D ($INST_D) on throwaway leaf '$CLUSTER_A'; supervisor $AGENT_S ($INST_S) on throwaway leaf '$CLUSTER_B'; both loopback on one host, no WAN hop"

# --- 0. two sessions, two identities ------------------------------------
# A collision here would collapse two sessions into one presence row and one
# durable, so it is asserted rather than assumed. It is also the guard on the
# scrape above: identical values mean the banner was read before both shims
# wrote it.
[[ "$INST_D" != "$INST_S" ]] \
  && ok "the two sessions minted distinct instances" \
  || bad "instance collision" "both sides minted $INST_D (d.err: $(grep -o 'instance [0-9A-Z]*' "$work/d.err" | head -1); s.err: $(grep -o 'instance [0-9A-Z]*' "$work/s.err" | head -1)); see director#62"

# --- 1. R-86/R-06/R-79: no rename between the tiers ---------------------
# The address the global tier knows must be the one the launcher minted, and
# the same string the local tier uses. Comparing the two presence rows is the
# check: same agent_id, same instance, no boundary rewrite.
grec="$("${HUB_B[@]}" kv get GLOBAL_PRESENCE "presence.$CLUSTER_B.supervisor.$INST_S" --raw 2>/dev/null || true)"
lrec="$("${LOC_B[@]}" kv get AGENT_STATE "presence.migrated.$AGENT_S.$INST_S" --raw 2>/dev/null || true)"
if [[ -n "$grec" && -n "$lrec" ]] \
   && [[ "$(jq -r .agent_id <<<"$grec")" == "$AGENT_S" ]] \
   && [[ "$(jq -r .instance <<<"$grec")" == "$INST_S" ]] \
   && [[ "$(jq -r .cluster  <<<"$grec")" == "$CLUSTER_B" ]] \
   && [[ "$(jq -r .role     <<<"$grec")" == "supervisor" ]]; then
  ok "no rename between tiers: global presence.$CLUSTER_B.supervisor.$INST_S carries agent_id $AGENT_S, and the local tier keys the same agent_id and instance"
else
  bad "no-rename across tiers" "global=${grec:-absent} local=${lrec:-absent}"
fi

# --- 2. the catalog advertises the address the far side must use --------
cat_s="$(s_rpc 2 tools/list '{}')"
jq -e --arg a "global://$CLUSTER_B/supervisor" \
  '.result.tools[] | select(.name=="send_message") | select(.description | contains($a))' <<<"$cat_s" >/dev/null \
  && ok "the supervisor advertises itself as global://$CLUSTER_B/supervisor" \
  || bad "supervisor catalog address"

# --- 3. the REQUEST: director to supervisor, nonce in the body ----------
NONCE="rt-$RANDOM-$RANDOM"
BODY="aae-orc-2vwae round trip, nonce $NONCE"
DIGEST="$(sha "$BODY")"
resp="$(d_call 3 send_message "$(jq -nc --arg t "global://$CLUSTER_B/supervisor" --arg b "$BODY" \
        '{to:$t,performative:"REQUEST",text:$b}')")"
ENQ_MS="$(now_ms)"
ack="$(text_of <<<"$resp")"
REQ_ID="$(jq -r '.message_id // ""' <<<"$ack")"
if [[ "$(iserr_of <<<"$resp")" != "true" ]] && [[ -n "$REQ_ID" ]] \
   && jq -e --arg s "GLOBAL_TO_$CLUSTER_B" 'select(.tier=="global" and .stream==$s and .sequence>0)' <<<"$ack" >/dev/null; then
  ok "the REQUEST is stored on the hub in GLOBAL_TO_$CLUSTER_B at sequence $(jq -r .sequence <<<"$ack") (this is the ACK, and check 7 proves it is not the answer)"
else
  bad "the REQUEST did not store" "$ack"
fi

# --- 4a. TRANSPORT: enqueued on the RECIPIENT's own durable -------------
# Observed before anyone polls, so it is a statement about the transport alone.
# If this passes and 4b fails, the bus did its job and the recipient is not
# draining; if this fails, the message was never going to arrive. Those are
# different defects with different owners and the sender cannot tell them apart.
# FAULT=no_consumer removes the recipient's durable before the send, so the
# message has nowhere to land. Check 4a must fail, which is the negative control
# for the transport assertion itself.
if [[ "${FAULT:-}" == "no_consumer" ]]; then
  echo "FAULT no_consumer: deleting the recipient's durable before the send"
  nats -s "nats://127.0.0.1:$LEAF_B_PORT" req --timeout 5s \
    "\$JS.$DOMAIN.API.CONSUMER.DELETE.GLOBAL_TO_$CLUSTER_B.$DURABLE_S" '{}' >/dev/null 2>&1 || true
fi
ci="$(consumer_info)"
pend="$(jq -r '.num_pending // -1' <<<"${ci:-{\}}" 2>/dev/null || echo -1)"
cfilt="$(jq -r '.config.filter_subject // ""' <<<"${ci:-{\}}" 2>/dev/null || echo "")"
if [[ "$pend" -ge 1 && "$cfilt" == "global.$CLUSTER_B.supervisor.inbox" ]]; then
  ok "transport: the REQUEST is enqueued on the recipient's own durable $DURABLE_S (num_pending $pend, filter $cfilt) before any poll"
else
  bad "transport: not enqueued on the recipient's durable" "num_pending=$pend filter=${cfilt:-none}"
fi

# --- 4b. DRAIN: the supervisor actually dequeues it ---------------------
# FAULT=not_drained is the case the supervisor measured on 2026-09-21: two
# dispatches accepted for delivery, correctly enqueued, and unread for four
# hours because the recipient sat at a prompt. The harness must call that a
# DRAIN failure and must NOT call it a transport failure or a pass.
got=""
if [[ "${FAULT:-}" == "not_drained" ]]; then
  echo "FAULT not_drained: the supervisor is parked and will not poll"
elif [[ -n "$REQ_ID" ]]; then
  for _ in 1 2 3 4 5 6; do
    out="$(RPC_TIMEOUT=40 s_call 4 wait_for_message '{"timeout_seconds":20}' | text_of)"
    [[ "$(jq -r '.message.message_id // ""' <<<"$out")" == "$REQ_ID" ]] && { got="$out"; break; }
  done
fi
DEQ_MS="$(now_ms)"
if [[ -n "$got" ]]; then
  echo "#   enqueued->dequeued gap: $((DEQ_MS - ENQ_MS))ms"
elif [[ "$pend" -ge 1 ]]; then
  bad "drain: the REQUEST was enqueued on the recipient's durable and never dequeued" "transport is NOT the defect here; the recipient did not poll (enqueued $((DEQ_MS - ENQ_MS))ms ago, still pending)"
fi
RECVD="$(jq -r '.message.content.data // ""' <<<"${got:-{\}}")"
if [[ -n "$got" ]] && [[ "$(jq -r .tier <<<"$got")" == "global" ]] \
   && [[ "$(jq -r '.message.performative' <<<"$got")" == "REQUEST" ]] && [[ "$RECVD" == "$BODY" ]]; then
  ok "the supervisor received the REQUEST on its global inbox, tier=global, content byte-identical"
else
  bad "the supervisor did not receive the REQUEST" "${got:-not received}"
fi

# --- 5. the RECEIPT: written by the RECIPIENT, echoing a digest ---------
# R-88. The supervisor digests what it actually received, so the answer can
# only be produced by something that read the body. It replies AGREE with
# in_reply_to set. This is the recipient's own write, on the recipient's own
# credential; nothing here is read back out of the sender's audit mirror.
RECVD_DIGEST="$(sha "$RECVD")"
# FAULT injects a deliberately broken receipt so the red side of this harness
# is reproducible rather than argued. selftest-roundtrip-receipts.sh runs each
# one and requires this script to FAIL. An instrument nobody has watched fail
# is not evidence (R-08).
case "${FAULT:-}" in
  no_in_reply_to)  reply_args="$(jq -nc --arg d "$RECVD_DIGEST" '{to:"global://director",performative:"AGREE",text:("receipt sha256=" + $d)}')" ;;
  wrong_digest)    reply_args="$(jq -nc --arg r "$REQ_ID" '{to:"global://director",performative:"AGREE",text:"receipt sha256=0000000000000000000000000000000000000000000000000000000000000000",in_reply_to:$r}')" ;;
  wrong_performative) reply_args="$(jq -nc --arg r "$REQ_ID" --arg d "$RECVD_DIGEST" '{to:"global://director",performative:"INFORM",text:("receipt sha256=" + $d),in_reply_to:$r}')" ;;
  *)               reply_args="$(jq -nc --arg r "$REQ_ID" --arg d "$RECVD_DIGEST" '{to:"global://director",performative:"AGREE",text:("receipt sha256=" + $d),in_reply_to:$r}')" ;;
esac
if [[ "${FAULT:-}" == "no_reply" ]]; then
  echo "FAULT no_reply: the supervisor is not answering; the finding-151 shape"
  resp='{"result":{"content":[{"text":"{}"}]}}'
elif [[ "${FAULT:-}" == "sender_writes_receipt" ]]; then
  # The forgery case aae-orc-2vwae names: the SENDER writes the receipt into
  # its own inbox. The director's leaf may publish global.director.>, so this
  # is reachable, and the authorship assertion in check 6 is what must catch it.
  echo "FAULT sender_writes_receipt: the director is writing its own receipt"
  resp="$(d_call 5 send_message "$(jq -nc --arg r "$REQ_ID" --arg d "$RECVD_DIGEST" \
          '{to:"global://director",performative:"AGREE",text:("receipt sha256=" + $d),in_reply_to:$r}')")"
else
  resp="$(s_call 5 send_message "$reply_args")"
fi
rack="$(text_of <<<"$resp")"
AGREE_ID="$(jq -r '.message_id // ""' <<<"$rack")"
if [[ "$(iserr_of <<<"$resp")" != "true" ]] && [[ -n "$AGREE_ID" ]] \
   && jq -e 'select(.stream=="GLOBAL_TO_DIRECTOR" and .tier=="global")' <<<"$rack" >/dev/null; then
  ok "the supervisor wrote its receipt into GLOBAL_TO_DIRECTOR at sequence $(jq -r .sequence <<<"$rack")"
else
  bad "the supervisor could not write a receipt" "$rack"
fi

# --- 6. the director RECEIVES the receipt, and it correlates ------------
# Every fail condition aae-orc-2vwae named in advance is asserted here.
# An empty AGREE_ID must never match an empty poll. On the first draft it did,
# and the no_reply fault reported six field failures instead of the one true
# one; the fault injection is what surfaced that.
got=""
if [[ -n "$AGREE_ID" ]]; then
  for _ in 1 2 3 4 5 6; do
    out="$(RPC_TIMEOUT=40 d_call 6 wait_for_message '{"timeout_seconds":20}' | text_of)"
    [[ "$(jq -r '.message.message_id // ""' <<<"$out")" == "$AGREE_ID" ]] && { got="$out"; break; }
  done
fi
if [[ -z "$got" ]]; then
  bad "the director never received a receipt" "five polls, no AGREE (the finding-151 shape)"
else
  m="$(jq -c .message <<<"$got")"
  perf="$(jq -r .performative <<<"$m")"
  irt="$(jq -r '.in_reply_to // ""' <<<"$m")"
  corr="$(jq -r '.correlation_id // ""' <<<"$m")"
  from="$(jq -r '.sender.agent_id // ""' <<<"$m")"
  echoed="$(jq -r '.content.data // ""' <<<"$m" | sed -n 's/.*sha256=\([0-9a-f]*\).*/\1/p')"
  [[ "$perf" == "AGREE" ]]      && ok "the answer is an AGREE"                         || bad "answer performative" "$perf"
  [[ "$irt"  == "$REQ_ID" ]]    && ok "the AGREE carries in_reply_to $REQ_ID"            || bad "in_reply_to missing or wrong" "${irt:-empty}"
  [[ "$corr" == "$REQ_ID" ]]    && ok "correlation_id binds to the REQUEST (R-88)"       || bad "correlation_id" "${corr:-empty}"
  [[ "$from" == "$AGENT_S" ]]   && ok "the receipt was written by the RECIPIENT ($from), not the sender" || bad "receipt authorship" "sender=${from:-empty}"
  [[ "$echoed" == "$DIGEST" ]]  && ok "the receipt echoes sha256 of the content the director sent"       || bad "content digest" "got=${echoed:-none} want=$DIGEST"
fi

# --- 6b. the receipt's RAW BYTES on the hub, read by a third principal --
# The admin nkey is neither shim. It fetches the stored message by sequence and
# this script parses the JSON itself, so in_reply_to, the sender and the digest
# are confirmed without either shim's decode in the path. This is the answer to
# "the harness and the thing it measures share a code path": it does not remove
# the shared binary, it removes the READ side of it from this one assertion.
rseq="$(jq -r '.sequence // ""' <<<"${rack:-{\}}")"
if [[ -z "$rseq" ]]; then
  nr "receipt raw bytes on the hub" "no sequence was returned for the receipt, so there is nothing to fetch"
else
  raw="$("${A[@]}" stream get GLOBAL_TO_DIRECTOR "$rseq" -j 2>/dev/null | jq -r '.data // .message.data // empty' | base64 -d 2>/dev/null || true)"
  if [[ -z "$raw" ]]; then
    bad "receipt raw bytes on the hub" "sequence $rseq could not be fetched as the admin"
  else
    r_irt="$(jq -r '.in_reply_to // ""' <<<"$raw")"
    r_from="$(jq -r '.sender.agent_id // ""' <<<"$raw")"
    r_dig="$(jq -r '.content.data // ""' <<<"$raw" | sed -n 's/.*sha256=\([0-9a-f]*\).*/\1/p')"
    if [[ "$r_irt" == "$REQ_ID" && "$r_from" == "$AGENT_S" && "$r_dig" == "$DIGEST" ]]; then
      ok "the stored receipt at GLOBAL_TO_DIRECTOR seq $rseq carries in_reply_to, the recipient's authorship and the right digest, read as the admin with neither shim decoding it"
    else
      bad "stored receipt bytes" "in_reply_to=${r_irt:-empty} sender=${r_from:-empty} digest=${r_dig:-none}"
    fi
  fi
fi

# --- 7. R-08 negative self-test: the ack must not be able to pass -------
# The instrument's own failure mode. A send to a cluster with no live
# supervisor still has to be refused or to produce no receipt; if this harness
# ever reports a pass here, it is counting "accepted for delivery" as an
# answer and every other check above is worthless.
# FAULT=neg_has_supervisor aims this probe at the cluster that DOES have a live
# supervisor and drives that supervisor to answer, so a receipt appears where
# check 7 asserts none can. Check 7 must then fail. Without it, check 7 is the
# one part of this instrument that is asserted but never demonstrated, which is
# the standard the other faults already meet, and it is the check that guards
# all the others.
neg_target="global://$CLUSTER_A/supervisor"
[[ "${FAULT:-}" == "neg_has_supervisor" ]] && { neg_target="global://$CLUSTER_B/supervisor"; echo "FAULT neg_has_supervisor: aiming the R-08 probe at a cluster that HAS a live supervisor"; }
resp="$(d_call 7 send_message "$(jq -nc --arg t "$neg_target" \
        '{to:$t,performative:"REQUEST",text:"R-08 negative self-test probe"}')")"
neg_ack="$(text_of <<<"$resp")"
neg_err="$(iserr_of <<<"$resp")"
neg_id="$(jq -r '.message_id // "x"' <<<"$neg_ack" 2>/dev/null || echo x)"
if [[ "${FAULT:-}" == "neg_has_supervisor" && "$neg_err" != "true" ]]; then
  # Drive the live supervisor to answer it, so a receipt genuinely exists.
  for _ in 1 2 3 4; do
    o="$(RPC_TIMEOUT=40 s_call 70 wait_for_message '{"timeout_seconds":20}' | text_of)"
    if [[ "$(jq -r '.message.message_id // ""' <<<"$o")" == "$neg_id" ]]; then
      s_call 71 send_message "$(jq -nc --arg r "$neg_id" '{to:"global://director",performative:"AGREE",text:"receipt sha256=deadbeef",in_reply_to:$r}')" >/dev/null
      break
    fi
  done
fi
# The comparison below is guarded on BOTH sides being non-empty, explicitly.
# When the shim hangs, text_of yields the literal string NO RESULT, jq errors on
# it, and jq's // default cannot fire because the alternative operator needs the
# input to PARSE before it can supply a default. Both sides then render empty
# and empty matches empty, which would set neg_receipt and report "a receipt
# appeared" when nothing did. neg_id above already falls back to a sentinel, but
# that is an incidental rescue and reads like one, so the -n guards are written
# out here: this is the third place today the same empty-matches-empty shape has
# appeared, after check 6 and the no_reply fault, and it should be legible
# rather than accidental.
neg_receipt=""
rpc_timeout_clear
if [[ "$neg_err" != "true" && -n "$neg_id" && "$neg_id" != "x" ]]; then
  for _ in 1 2 3; do
    out="$(RPC_TIMEOUT=15 d_call 8 wait_for_message '{"timeout_seconds":6}' | text_of)"
    cand="$(jq -r '.message.in_reply_to // ""' <<<"$out" 2>/dev/null || true)"
    [[ -n "$cand" && "$cand" == "$neg_id" ]] && { neg_receipt="$out"; break; }
  done
fi
# Green by absence is the trap here: zero delivered and zero ATTEMPTED look
# identical. So neither branch below is allowed to pass on absence alone. The
# refusal branch passes on a named refusal, which is positive evidence. The
# accepted branch must show the attempt actually landed somewhere, by finding
# the acknowledged sequence in the stream, before "no receipt" means anything.
if [[ -n "$neg_receipt" ]]; then
  bad "R-08 negative self-test" "a receipt appeared for this probe; the harness is measuring something other than delivery"
elif rpc_timed_out; then
  bad "R-08 negative self-test" "the receipt poll TIMED OUT rather than returning empty, so 'no receipt' here is a hung shim and not an absence, and it cannot be scored either way"
elif [[ "$neg_err" == "true" ]]; then
  # The reason is quoted rather than summarised, and quoted whole. It used to
  # be cut at 70 characters, which ended the line mid-word ("no s") and printed
  # a fragment where the output claims to show the named reason.
  ok "R-08: a send with no live supervisor is refused up front with a named reason, and no receipt is counted ($(text_of <<<"$resp" | tr '\n' ' ' | head -c 200 | sed 's/  */ /g; s/ *$//'))"
else
  nseq="$(jq -r '.sequence // ""' <<<"$neg_ack")"
  nstr="$(jq -r '.stream // ""' <<<"$neg_ack")"
  if [[ -n "$nseq" && -n "$nstr" ]] && "${A[@]}" stream get "$nstr" "$nseq" -j >/dev/null 2>&1; then
    ok "R-08: the send was accepted and IS stored at $nstr seq $nseq (the attempt is evidenced, not assumed), and no receipt followed, so the harness scores it not-delivered"
  else
    bad "R-08 negative self-test" "the send neither refused nor produced a locatable stored message, so 'no receipt' here is absence of evidence and cannot be scored"
  fi
fi

# --- 8. the reverse leg (aae-orc-2vwae clause d) ------------------------
NONCE2="rt-rev-$RANDOM"
BODY2="aae-orc-2vwae reverse leg, nonce $NONCE2"
DIGEST2="$(sha "$BODY2")"
resp="$(s_call 9 send_message "$(jq -nc --arg b "$BODY2" '{to:"global://director",performative:"REQUEST",text:$b}')")"
REQ2="$(text_of <<<"$resp" | jq -r '.message_id // ""')"
got=""
for _ in 1 2 3 4 5 6; do
  out="$(RPC_TIMEOUT=40 d_call 10 wait_for_message '{"timeout_seconds":20}' | text_of)"
  [[ "$(jq -r '.message.message_id // ""' <<<"$out")" == "$REQ2" ]] && { got="$out"; break; }
done
if [[ -z "$got" ]]; then
  bad "reverse leg: the director never received the supervisor's REQUEST"
else
  r2="$(sha "$(jq -r '.message.content.data' <<<"$got")")"
  resp="$(d_call 11 send_message "$(jq -nc --arg r "$REQ2" --arg d "$r2" \
          '{to:"global://'"$CLUSTER_B"'/supervisor",performative:"AGREE",text:("receipt sha256=" + $d),in_reply_to:$r}')")"
  A2="$(text_of <<<"$resp" | jq -r '.message_id // ""')"
  got2=""
  if [[ -n "$A2" ]]; then
    for _ in 1 2 3 4 5 6; do
      out="$(RPC_TIMEOUT=40 s_call 12 wait_for_message '{"timeout_seconds":20}' | text_of)"
      [[ "$(jq -r '.message.message_id // ""' <<<"$out")" == "$A2" ]] && { got2="$out"; break; }
    done
  fi
  if [[ -n "$got2" ]] \
     && [[ "$(jq -r '.message.in_reply_to' <<<"$got2")" == "$REQ2" ]] \
     && [[ "$(jq -r '.message.sender.agent_id' <<<"$got2")" == "$AGENT_D" ]] \
     && [[ "$(jq -r '.message.content.data' <<<"$got2" | sed -n 's/.*sha256=\([0-9a-f]*\).*/\1/p')" == "$DIGEST2" ]]; then
    ok "reverse leg: the director's receipt returns with in_reply_to, the director's authorship and a matching digest"
  else
    bad "reverse leg receipt" "${got2:-not received}"
  fi
fi

# --- 9. R-95 asymmetry still holds under the credential -----------------
# A cluster leaf may publish upward and must not be able to read the fleet
# inbox. If this ever passes, the receipt in check 6 could have been read
# rather than received, and the whole instrument degrades to verify-global-shim's
# audit-mirror shape.
# The positive control is the whole check. Stream info failing on a timeout, a
# wrong js-domain, a dropped leaf or a dead hub is indistinguishable from the
# permission denial this is trying to prove, so the denial has to be shown
# SPECIFIC: the same credential, in the same call shape, must succeed on its own
# cluster's stream in the same breath. Without that, "it failed" proves nothing.
if [[ "${FAULT:-}" == "grant_asymmetry_broken_leaf_dead" ]]; then
  echo "FAULT grant_asymmetry_broken_leaf_dead: killing the $CLUSTER_B leaf so check 9 fails at its positive control, not at its assertion"
  kill -9 "$LEAF_B_PID" 2>/dev/null || true
  sleep 1
fi
if ! "${HUB_B[@]}" stream info "GLOBAL_TO_$CLUSTER_B" >/dev/null 2>&1; then
  bad "R-95 asymmetry" "the positive control failed: the $CLUSTER_B credential cannot read its OWN stream either, so a refusal on the director stream would prove nothing (leaf down, wrong domain or dead hub)"
elif "${HUB_B[@]}" stream info GLOBAL_TO_DIRECTOR >/dev/null 2>&1; then
  bad "R-95 asymmetry" "the $CLUSTER_B credential can read GLOBAL_TO_DIRECTOR"
else
  ok "R-95 asymmetry holds and the refusal is specific: the $CLUSTER_B credential reads GLOBAL_TO_$CLUSTER_B and is refused GLOBAL_TO_DIRECTOR on the same credential"
fi

# --- 10. BEAT-C orderly exit (aae-orc-5lkxr case (a)) -------------------
# The teardown order exists for this: the shims are stopped while the hub is
# still up, so an orderly deregister has somewhere to land. Measured against
# the 90s bucket TTL, because "the row went away" is only interesting if it
# went away FASTER than the TTL would have taken it.
skey="presence.$CLUSTER_B.supervisor.$INST_S"
"${HUB_B[@]}" kv get GLOBAL_PRESENCE "$skey" --raw >/dev/null 2>&1 \
  && before_exit=yes || before_exit=no
if [[ "$before_exit" != yes ]]; then
  nr "aae-orc-5lkxr (a) orderly exit" "the supervisor row was already absent before the exit, so the delete cannot be timed"
else
  # THE FOURTH SITE. This check reads a DELETION, so its evidence is a read
  # that fails, and a read failing for any other reason (dead hub, dropped
  # leaf, expired credential, timeout) is indistinguishable from the row being
  # gone. That is the same absence-as-evidence class as checks 7 and 9, and it
  # does NOT route through the rpc helpers, so the transport-level timeout
  # marker does not cover it. It needs its own positive control: the same
  # credential must still be able to READ the bucket, proved against the
  # director's row, which is live because only the supervisor was killed.
  dkey="presence.director.$INST_D"
  # FAULT=leaf_dead_before_beatc is this check's acceptance test: kill the leaf
  # so every read fails, and the row reads "absent" for a reason that has
  # nothing to do with BEAT-C. Without the positive control below this produced
  # a PASS at the LAST assertion of an otherwise green run, which is the only
  # way a member of this class can green the whole instrument.
  if [[ "${FAULT:-}" == "leaf_dead_before_beatc" ]]; then
    echo "FAULT leaf_dead_before_beatc: killing the $CLUSTER_B leaf before the BEAT-C check"
    kill -9 "$LEAF_B_PID" 2>/dev/null || true
    sleep 1
  fi
  kill -TERM "$S_PID" 2>/dev/null || true
  t0=$SECONDS; gone=no
  for _ in $(seq 1 40); do
    "${HUB_B[@]}" kv get GLOBAL_PRESENCE "$skey" --raw >/dev/null 2>&1 || { gone=yes; break; }
    sleep 0.25
  done
  elapsed=$((SECONDS - t0))
  if [[ "$gone" == yes ]] && ! "${HUB_B[@]}" kv get GLOBAL_PRESENCE "$dkey" --raw >/dev/null 2>&1; then
    bad "aae-orc-5lkxr (a) orderly exit" "unscoreable: the supervisor's row read as absent, but the same credential cannot read the live director row either, so the bucket is unreadable rather than the row deleted"
  elif [[ "$gone" == yes && $elapsed -lt 10 ]]; then
    ok "aae-orc-5lkxr (a): an orderly exit vacated the global presence row in ${elapsed}s, well inside the 90s TTL, so the clearance is BEAT-C-shaped and not TTL-shaped; read back through the $CLUSTER_B leaf against the hub-domain bucket, with the live director row still readable on the same credential"
  elif [[ "$gone" == yes ]]; then
    bad "aae-orc-5lkxr (a) orderly exit" "the row cleared only after ${elapsed}s, which is TTL-shaped, not BEAT-C-shaped"
  else
    bad "aae-orc-5lkxr (a) orderly exit" "the row survived the orderly exit; BEAT-C did not delete it and it is waiting on the 90s TTL"
  fi
fi

# --- 11. the leg this gate does NOT run ---------------------------------
# The most important line in the output, and it was missing until the re-review
# found it. Everything above runs against a hub this script builds on loopback.
# The REAL cross-host attestation, which is what aae-orc-2vwae's title asks for,
# is not attempted here at all, and a reader who sees "N passed, 0 failed" with
# three unrelated NOT RUN lines would have no way to learn that. An unnamed
# omission is worse than a named one (finding-157), so it is named first.
nr "aae-orc-2vwae cross-host attestation against live seats"    "NOT attempted. Not merely skipped: no session on a cluster can publish into a cluster inbox (every team user's global publish allow is exactly global.director.inbox), so the live leg must be INITIATED BY THE DIRECTOR SEAT and cannot be driven from the cluster under test. What passes above is the receipt semantics against a hub this script builds, not the real WAN hop"

# --- sibling checks this rig cannot honestly answer ---------------------
nr "aae-orc-6vy9x read side" "the leak is in marvel's rendered authorization.conf; this rig writes its own leaf conf, so it cannot measure marvel's renderer. Run verify-global-grants.sh against a marvel-managed broker"
nr "aae-orc-5lkxr (b) ungraceful exit and (c) partition read" "SIGKILL timing against the 90s TTL and a presence read with the leaf down are lifecycle cases this rig does not drive; case (a) is measured above"
nr "aae-orc-8gp9f identity across a shift" "out of scope for this ticket and non-blocking for the stage, per av2v1"

echo
# Item D. The checks below are asserted but NOT negatively controlled. The
# header said so since the first draft, seven hundred lines above the line a
# gate reader actually reads, which is the same defect as an unnamed NOT RUN.
#
# The RULE is printed beside the list on purpose, because the list is an
# enumeration and an enumeration grows whenever someone adds a check without a
# fault. That puts the obligation on every future check author and nothing
# checks it, which is why the first version of this line said three when the
# answer was five. With the rule stated, a reader can re-derive the set from
# the selftest's faults array instead of trusting this string, and a wrong list
# becomes visible rather than authoritative.
#
# It is also cross-checked rather than merely stated: selftest-roundtrip-
# receipts.sh derives the same set, from this file's own section headers minus
# the checks its faults target, and FAILS if it disagrees with this line. Add a
# check and the red side goes red until either a fault or this string catches
# up.
echo "UNCONTROLLED checks 0, 1, 2, 3 and 8 (instance distinctness, no-rename, catalog address, REQUEST stored, reverse leg) are asserted but have no red run. The rule: a check with no fault aimed at it in the selftest's faults array has no red run"
echo "$pass passed, $fail failed, $notrun not run"
[[ $fail -eq 0 ]]

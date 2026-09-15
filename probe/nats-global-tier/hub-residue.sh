# Ledger of what a verify run stores on the SHARED hub streams, and the removal
# of it. Sourced, not executed. verify-global-shim.sh uses it;
# verify-residue-cleanup.sh exercises it against a throwaway broker.
#
# Why this exists (director#38). The hub streams are real inboxes. The global
# inbox consumer is DeliverAll, so anything a verify run leaves on
# GLOBAL_TO_<cluster> is replayed to the NEXT real supervisor cast on that
# cluster, inside the streams' 24h max age. Draining with a consumer does not
# help: a consumer ack advances that consumer, it does not remove the message,
# and a fresh durable starts at the beginning again.
#
# The caller provides, as globals: HUB (the nats invocation array, already
# pointed at the local broker and carrying --js-domain), and DOMAIN (the hub's
# JetStream domain). jq is required.
#
# Two rules the removal obeys:
#
#   1. Delete by SEQUENCE, never purge. `stream purge --seq` removes everything
#      up to a sequence, which on a shared inbox means other principals' real
#      messages. The run removes exactly the sequences it created.
#   2. A principal may remove from the stream it CONSUMES (its own inbox) and
#      never from the stream it PUBLISHES to. So a cluster can clear its own
#      GLOBAL_TO_<cluster> and cannot reach into GLOBAL_TO_DIRECTOR. That keeps
#      brief 8 section 4.1's asymmetry intact, and it means the outbound legs
#      of this run are reported rather than removed.
#
# Both are enforced at the hub by the credential, not here. When the grant is
# absent the delete request draws no responder and the run says so, loudly,
# with the sequences: the script never claims a clean hub it did not leave.

residue_stream=(); residue_seq=(); residue_what=()

# hub_record <stream> <seq> <what>
hub_record() { residue_stream+=("$1"); residue_seq+=("$2"); residue_what+=("$3"); }

# hub_publish <subject> <payload> <what>
# Publishes to a hub-backed subject and records the sequence JetStream assigns.
# The PubAck is read from the request reply rather than from the CLI's own
# output, because the ack is the protocol and the output is formatting.
#
# It sets HUB_LAST_SEQ and HUB_LAST_STREAM rather than printing them. That is
# deliberate and it matters: a caller writing seq=$(hub_publish ...) would run
# this in a subshell, the ledger would be appended to in that subshell, and the
# parent would clean up an empty ledger while reporting success. Read the
# globals instead.
#
# Returns nonzero if the ack could not be read, which is the case worth knowing
# about: the message may be stored and unrecorded.
hub_publish() {
  local subject="$1" payload="$2" what="$3" ack seq stream
  HUB_LAST_SEQ=""; HUB_LAST_STREAM=""
  ack="$("${HUB[@]}" req "$subject" "$payload" --timeout 10s 2>/dev/null | grep -o '{.*}' | head -1)" || true
  seq="$(printf '%s' "${ack:-}" | jq -r '.seq // empty' 2>/dev/null)" || true
  stream="$(printf '%s' "${ack:-}" | jq -r '.stream // empty' 2>/dev/null)" || true
  if [[ -n "${seq:-}" && -n "${stream:-}" ]]; then
    hub_record "$stream" "$seq" "$what"
    HUB_LAST_SEQ="$seq"; HUB_LAST_STREAM="$stream"
    return 0
  fi
  hub_record "UNKNOWN" "?" "$what (the publish ack was not read, so the message may be stored unrecorded)"
  return 1
}

# hub_drop
# Call it in the current shell, never in a command substitution, for the same
# reason hub_publish sets globals: RESIDUE_LEFT would be set in the subshell and
# the caller would read a stale zero. Redirect to a file if the output is needed.
# Removes every recorded message it is permitted to remove. Sets RESIDUE_LEFT
# to the number still on the hub afterwards and prints them. Never fails the
# run: the grant may not be live yet, and a red verify script is an ignored
# verify script. The disclosure is the point.
hub_drop() {
  RESIDUE_LEFT=0
  local total=${#residue_stream[@]}
  [[ "$total" -eq 0 ]] && return 0
  local i stream seq what resp left=""
  for (( i=0; i<total; i++ )); do
    stream="${residue_stream[$i]}"; seq="${residue_seq[$i]}"; what="${residue_what[$i]}"
    if [[ "$stream" == UNKNOWN ]]; then
      left+="  $stream seq $seq  $what"$'\n'; RESIDUE_LEFT=$((RESIDUE_LEFT+1)); continue
    fi
    resp="$("${HUB[@]}" req '$JS.'"$DOMAIN"'.API.STREAM.MSG.DELETE.'"$stream" "{\"seq\":$seq}" \
            --timeout 10s 2>/dev/null | grep -o '{.*}' | head -1)" || true
    if [[ -n "${resp:-}" ]] && printf '%s' "$resp" | jq -e '.success == true' >/dev/null 2>&1; then
      continue
    fi
    if [[ -z "${resp:-}" ]]; then
      left+="  $stream seq $seq  $what (no responder: this credential holds no STREAM.MSG.DELETE for $stream)"$'\n'
    else
      left+="  $stream seq $seq  $what ($(printf '%s' "$resp" | jq -r '.error.description // "delete refused"'))"$'\n'
    fi
    RESIDUE_LEFT=$((RESIDUE_LEFT+1))
  done
  if [[ "$RESIDUE_LEFT" -gt 0 ]]; then
    printf '\nRESIDUE: %d message(s) this run stored are still on the hub:\n%s' "$RESIDUE_LEFT" "$left" >&2
    printf 'They expire with the streams 24h max age. Until then a fresh DeliverAll\n' >&2
    printf 'consumer replays them, so a real cast on this cluster would see them.\n' >&2
  fi
  return 0
}

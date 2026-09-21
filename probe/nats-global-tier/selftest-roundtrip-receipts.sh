#!/usr/bin/env bash
# The red side of verify-roundtrip-receipts.sh (aae-orc-2vwae, R-08).
#
# A gate that has only ever been observed passing is not evidence. This driver
# runs the round trip five more times, each with one deliberate defect injected
# at the receipt, and requires the harness to FAIL on every one and to fail for
# the stated reason. If any fault run comes back green, the harness is counting
# something other than delivery, which is the exact failure R-08 names and the
# whole reason aae-orc-2vwae was filed as instrument-first.
#
# The faults are the fail conditions aae-orc-2vwae named in advance:
#   no_in_reply_to         an AGREE with no in_reply_to
#   sender_writes_receipt  a receipt that is the sender's own write
#   no_reply               five polls and no receipt (the finding-151 shape)
#   wrong_digest           an answer from something that did not read the body
#   wrong_performative     an answer that is not an AGREE
#   not_drained            enqueued correctly and never polled: the drain case
#                          the supervisor measured on 2026-09-21, which a
#                          one-event harness would misreport as a transport bug
#   no_consumer            the recipient has no durable, so the transport
#                          assertion itself has a negative control
#   grant_asymmetry_broken the cluster leaf is granted read on the director
#                          stream, so the check that keeps this harness from
#                          decaying into reading its own receipt is controlled
#
# Each run stands up its own hub and leaves on its own ports, so the runs are
# sequential and independent. Budget about 30s per fault.
#
# Usage: probe/nats-global-tier/selftest-roundtrip-receipts.sh
# Exit nonzero if any fault failed to be caught.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HARNESS="$here/verify-roundtrip-receipts.sh"
[[ -x "$HARNESS" ]] || { echo "not executable: $HARNESS"; exit 2; }

# fault -> a string that must appear in the FAIL line it provokes
faults=(no_in_reply_to sender_writes_receipt no_reply wrong_digest wrong_performative not_drained no_consumer grant_asymmetry_broken)
expect_no_in_reply_to="in_reply_to missing or wrong"
expect_sender_writes_receipt="receipt authorship"
expect_no_reply="never received a receipt"
expect_wrong_digest="content digest"
expect_wrong_performative="answer performative"
expect_not_drained="drain: the REQUEST was enqueued on the recipient's durable and never dequeued"
expect_no_consumer="transport: not enqueued on the recipient's durable"
expect_grant_asymmetry_broken="R-95 asymmetry"

caught=0; missed=0
for f in "${faults[@]}"; do
  want_var="expect_$f"; want="${!want_var}"
  out="$(FAULT="$f" "$HARNESS" 2>&1)" && rc=0 || rc=$?
  if [[ $rc -eq 0 ]]; then
    echo "MISSED $f -- the harness passed with this defect injected"
    missed=$((missed+1))
  elif grep -q "FAIL .*$want" <<<"$out"; then
    echo "CAUGHT $f -- $(grep -m1 "FAIL .*$want" <<<"$out")"
    caught=$((caught+1))
  else
    echo "MISSED $f -- the harness failed, but not for the stated reason (wanted a FAIL naming \"$want\")"
    grep '^FAIL' <<<"$out" | sed 's/^/        /'
    missed=$((missed+1))
  fi
done

echo
echo "$caught caught, $missed missed"
[[ $missed -eq 0 ]]

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

# fault -> a string that must appear in the FAIL line it provokes.
#
# THE STRING MUST NAME THE BRANCH, NOT THE CHECK. bad() renders
# "FAIL <label> -- <detail>" and the driver greps "FAIL .*$want", so an expect
# string that is the LABEL matches EVERY failure branch under that label, and a
# fault provoking the wrong branch still reports CAUGHT. A label discriminates
# only for a check that can fail exactly one way. Checks 7 and 9 each grew extra
# unscoreable branches while this PR was in review, which silently widened their
# labels; the expect strings below quote the DETAIL of the one branch each fault
# is supposed to provoke.
#
# That is the same defect as the one this harness exists to catch, one layer up:
# adding an unscoreable branch to fix absence-as-evidence on the green side
# widens any matcher that names that check by label on the red side. The remedy
# has its own failure mode.
faults=(no_in_reply_to sender_writes_receipt no_reply wrong_digest wrong_performative not_drained no_consumer grant_asymmetry_broken neg_has_supervisor leaf_dead_before_beatc)
expect_no_in_reply_to="in_reply_to missing or wrong"
expect_sender_writes_receipt="receipt authorship"
expect_no_reply="never received a receipt"
expect_wrong_digest="content digest"
expect_wrong_performative="answer performative"
expect_not_drained="drain: the REQUEST was enqueued on the recipient's durable and never dequeued"
expect_no_consumer="transport: not enqueued on the recipient's durable"
expect_grant_asymmetry_broken="can read GLOBAL_TO_DIRECTOR"
expect_neg_has_supervisor="a receipt appeared for this probe"
expect_leaf_dead_before_beatc="unscoreable: the supervisor's row read as absent"

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

# --- the mis-provocation: a fault that must NOT be caught ----------------
# The second half of the acceptance test. The totals above only show the eight
# that were always right still work. This shows the matcher now discriminates:
# grant_asymmetry_broken injected WITH THE LEAF DEAD makes check 9 fail at its
# positive control rather than at its assertion, so the expect string must NOT
# match. If this reports CAUGHT, the matcher is keyed on the label again and the
# fix has not landed, whatever the totals say.
echo
mp_out="$(FAULT=grant_asymmetry_broken_leaf_dead "$HARNESS" 2>&1)" && mp_rc=0 || mp_rc=$?
if [[ $mp_rc -eq 0 ]]; then
  echo "MIS-PROVOCATION INCONCLUSIVE -- the harness passed, so no FAIL line was produced to match against"
  mp_bad=1
elif grep -q "FAIL .*$expect_grant_asymmetry_broken" <<<"$mp_out"; then
  echo "MIS-PROVOCATION FAILED -- the wrong branch satisfied expect_grant_asymmetry_broken, so the matcher is keyed on the label"
  grep '^FAIL' <<<"$mp_out" | sed 's/^/        /'
  mp_bad=1
else
  echo "MIS-PROVOCATION OK -- the positive-control branch did NOT satisfy expect_grant_asymmetry_broken"
  echo "        provoked: $(grep -m1 '^FAIL' <<<"$mp_out")"
  mp_bad=0
fi

[[ $missed -eq 0 && $mp_bad -eq 0 ]]

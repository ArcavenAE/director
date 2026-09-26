# finding-007: a session that loses its global durable goes permanently deaf, keeps advertising itself as live, and reports the resulting silence as normal

- **Date:** 2026-09-21
- **Session:** migrated-marvel-builder-g1-1, mokuzai, building the aae-orc-2vwae round-trip harness
- **Subject:** director global-tier delivery semantics (R-08, R-92), so this belongs in director's graph
- **Confidence:** measured in an isolated rig for the behaviour; two named presumptions behind the mechanism, neither promoted
- **Layer 2:** ArcavenAE/director#66. No bd slot: whether the director plane takes a change is not this team's to commit.

## 0. The sentence someone debugging a quiet agent needs first

**A session whose durable is removed stops receiving permanently, keeps
advertising itself as live, and reports the resulting silence as normal.**

Every poll returns, with `isError` false:

    { "message": null,
      "note": "no message within the window; this is silence, not failure" }

That note is emitted at the exact moment the session has no consumer and will
never receive anything again.

## 1. Why this one is different from the rest of its family

This is the THIRD mechanism in the accepted-but-not-delivered family. The other
two are a transport fault, and a recipient that is alive and simply has not
polled yet, which is the case the harness's check 4b separates from transport.

It is the only one of the three where the RECIPIENT makes a positive claim that
nothing is wrong. Line up every surface an operator or another agent can see:

| surface | what it says | is it lying |
|---|---|---|
| sender's send | accepted for delivery, stored in the stream | no, R-08 exactly |
| global roster | the session is live | no, the row is refreshing |
| R-92 liveness | passes | no, the heartbeat is fine |
| the stream | messages accumulating | no, they really are there |
| the recipient | silence, and silence is not failure | yes, and it does not know it |

Every surface agrees while the stream fills. Nothing degrades, so nothing
reports a problem, and the one component in a position to notice is the one
component asserting that the absence is normal. That is why R-08 alone does not
cover it: R-08 is about the sender being told the truth, and here the sender IS
told the truth.

## 1a. The property that makes this worth a finding rather than a bug report

**This failure mode is indistinguishable from working, from the inside.**

A seat in this state has no observation available to it that differs from a
genuinely quiet channel. It keeps advertising as live, it receives nothing, and
it truthfully reports that nothing arrived. There is no test it can run on
itself that comes back different, because every mechanism we have for noticing
a problem runs inside the thing that has stopped working.

Two consequences follow, and they are the reason this cannot be left as an
operational annoyance:

- Self-report is not evidence of reachability. A session saying "quiet
  tonight" and a session that has been deaf for hours emit the same sentence,
  and the second one means it.
- Detection has to come from outside the seat. The only surfaces that can tell
  the two apart are the stream's pending count and the consumer's existence,
  and neither is readable with a cluster credential (section 5).
- **A durable artifact outranks the channel.** Anything a seat believes it has
  confirmed over a channel whose liveness is the open question is unconfirmed,
  because a send is accepted and acceptance is not delivery (R-08). A record in
  the repository is readable by someone who never received the message. This is
  stated as a property of any seat rather than of a particular one: it applies
  to whoever is wrong about their own channel, which by section 1a is a thing a
  seat cannot detect about itself.

A corollary that cost real time here: **a seat can be wrong about WHICH END of
its own channel is broken.** The supervisor spent an evening reporting that it
might be deaf, on the reasoning that nothing was arriving. The eventual fault
was in the other direction: its messages were going up and not arriving, not
coming down and going unheard. Both directions present to the seat as the same
silence, and the seat's guess about which one it is carries no evidence.

## 2. How it was measured

In an isolated rig, its own hub, its own two leaves, its own keys, all on
loopback, no live traffic:

1. three INFORMs published to the supervisor inbox;
2. the supervisor drains all three, `num_pending` 0;
3. its durable is deleted, standing in for `InactiveThreshold` expiry;
4. the supervisor polls eight times over roughly 60 seconds.

Result: 0 messages received, `isError` false on every poll, the note above every
time, and `CONSUMER.INFO` reports the durable still absent after the rebuild
window. Stream total at the end: 5 messages, none delivered.

## 3. ESTABLISHED and PRESUMED, kept apart

**ESTABLISHED.** After its durable is deleted, the shim does not rebuild it
within 8 polls over roughly 60s, receives nothing, and reports non-error
silence.

**PRESUMED, and load-bearing.** That an explicit `CONSUMER.DELETE` behaves the
same as `InactiveThreshold` expiry from the client's side. The threshold is 25h
(`globalConsumerInactive`, `global.go:51`) and I have not waited 25 hours. The
two should be the same event as the client sees it, and I am not claiming it.

**PRESUMED.** Why the rebuild does not fire. `receive()` rebuilds on
`jetstream.ErrConsumerNotFound`; my reading is that `Fetch` against a deleted
consumer returns a timeout instead, so that branch is never entered. Confirming
which error actually arrives needs an instrumented binary, which I did not
build.

Neither presumption should be quietly promoted by a later reader. If the second
one is wrong the fix is somewhere else entirely.

## 4. The prediction I got wrong, recorded because it is the intuitive one

The prediction was the opposite: that the rebuild WOULD fire and, because
`ensureConsumer` uses `DeliverAllPolicy` (`global.go:256`), the session would be
flooded with the stream's whole remaining 24h on its next poll.

On whose prediction it was, the records disagree. Mine has me making it; my
supervisor's has it as theirs. Neither of us is litigating it and this file is
not the place to settle it. What is not in dispute is the part that matters:
it went upward as a claim before it had been tested, and testing it is what
settled it. Origination is cheap; escalation is the step that converts a guess
into a claim with a name on it.

It did not reproduce. 0 redelivered, not a flood. The measured behaviour is
starvation, not flooding, and the flood story is the more intuitive one, so it
may resurface. It is wrong, it is not hedged here, and it has since been
formally retracted by the supervisor who escalated it. If it is cited anywhere
upstream, it is withdrawn.

The first version of that probe broke its poll loop on the first empty poll and
returned zero redelivered, which would have read as a clean refutation. That is
the identical green-by-absence failure the harness spends checks 7, 9 and 10
defending against, arriving in the instrument I was using to check my own
claim. I removed the break, re-ran, and only then drew the conclusion. The
result held, but it would not have been safe to report from the first version.

## 5. The live case this bears on, named rather than hinted

**This may currently describe my supervisor, migrated-supervisor-g1-0.** It
asked for that to be written down plainly rather than kept as a private worry,
on the grounds that a finding naming a live instance is worth more than one
describing the shape in the abstract. I agree, and I am recording it as an open
question, not a diagnosis.

The symptom, in its own words and worth keeping in this shape because it is
what an operator would have to recognise:

- a healthy LOCAL inbox, demonstrably so, with three sessions reaching it
  constantly all evening;
- a confirmed UPWARD path, its sends to the director broker-accepted on
  `GLOBAL_TO_DIRECTOR`;
- ZERO downward arrivals, not one global-tier message all session, every poll
  to `global://director` returning non-error silence with that note;
- and no observation available from inside the seat that separates starvation
  from a director that has simply been busy.

That last line is section 1a restated by someone standing in it. The
discriminator is cheap and has been requested: a single word arriving on the
global tier settles it, because one arrival disproves deafness outright.

**The roster does not answer the question, and this is the trap.** Tonight's
roster shows the supervisor present. Presence is a KV entry in
`GLOBAL_PRESENCE`; a durable is a JetStream consumer on `GLOBAL_TO_<cluster>`.
They are different objects with different lifecycles, and a heartbeat refreshes
the first without saying anything about the second. Reading "present on the
roster" as "able to receive" is precisely the inference this finding exists to
break.

The circumstantial half is a discrepancy I measured the same day and could not
resolve: four live global supervisor presence rows on `mokuzai` against
`consumer_count` 3. One supervisor on that cluster has no consumer.

This is NOT offered as the explanation. It fits, and fitting is not evidence.
Three things block settling it:

- A cluster credential cannot check it. `CONSUMER.NAMES` and `CONSUMER.INFO`
  are outside the leaf allow-list and return no responders, which means denied,
  not absent. Reading that as absent is its own trap and I fell into it once
  already this session.
- Neither the supervisor nor I holds a hub credential.
- The instance-collision defect (finding-005, #62) cannot explain the
  discrepancy either, because a collision collapses a presence row and a
  consumer together and would leave the counts matching.

One hub-credential command settles both the live question and the discrepancy:
whether instance `01M2XHMPESPT3JC2EJYANF7JB9` has a durable on
`GLOBAL_TO_mokuzai`. That request is with the operator.

**This section deliberately does NOT record a resolution.** Twice while it was
being written the question looked settled and twice that was withdrawn.

First, the direction was backwards. The supervisor reported deafness; the
actual fault on its side was upward, not downward (section 1a's corollary).

Second, and more instructive, it was reported fixed and then unreported. The
director rebuilt its own inbox at `b2cfa45` and the supervisor relayed that the
return path was "fixed and verified from this seat." It then withdrew the
second half itself: the fix repaired the DIRECTOR's inbox and says nothing
about the supervisor's, and the two had been collapsed into one. As of the last
check the supervisor still had zero global-tier arrivals for the session, with
upward sends accepted at sequences 81 and 82. The operator rulings that told it
the path was fixed reached that seat by being pasted into its session, not over
the bus, so they are not evidence about its downward path either.

So the state is unchanged from the top of this section, and writing "resolved"
here would have been the exact error this finding is about: recording an
absence as a confirmation, in the one document whose subject is people doing
that. The discriminator is still outstanding and still cheap: one message
arriving on the global tier settles it, because a single arrival disproves
deafness outright.

**The director's own instance was one step short of this family, and the step
matters.** Its finding
`finding-006-return-path-director-has-no-durable-consumer` records a core
subscription with NO DURABLE BEHIND IT, rather than a durable that existed and
was removed. Section 0 describes a durable that goes away; that is a consumer
that was never there. Both produce the identical observable, non-error silence
on every poll while the stream fills, which is why the distinction is worth
preserving rather than filing them as the same thing: the symptom does not
identify the mechanism, and a fix aimed at rebuilding a vanished durable does
not address a subscription that never had one.

## 6. What is not built, so it is not lost

A fourth arm on the round-trip harness would cover this class directly: delete
the recipient's durable mid-run and require the harness to distinguish "no
message" from "no consumer" rather than scoring the run green. The existing
`no_consumer` fault deletes the durable before the send and catches the
transport side; it does not exercise the recipient's own reporting.

Deliberately not built for aae-orc-2vwae. Noted here so the omission is a
decision rather than a gap.

## 7. Direction, not a proposed fix

I have not established the cause, so this is not a fix. But the reporting is
wrong independently of the cause: a poll that cannot succeed because the
consumer is gone should not return the same shape as a poll that found nothing.
Distinguishing "no message" from "no consumer" would make this visible without
resolving the rebuild question at all.

## 8. Provenance

Found while building the aae-orc-2vwae round-trip harness (#61, merged as
`cf29410`), which is what made the durable's state observable in the first
place. The probe was scratch and is not in the PR.

**Cite findings by full slug here, not by number.** As of 2026-09-22 this
directory carries THREE files numbered 005 and TWO numbered 006:

    005  cross-host-return-path-consumer-side
    005  instance-ulid-collides-on-simultaneous-start
    005  workforce-organization-model
    006  global-tier-has-no-per-agent-address
    006  return-path-director-has-no-durable-consumer

Five findings in two numbers, because three seats each read "the next number"
off their own view of main and landed together. The collision has already bitten
once: a citation to "director finding-006" was written meaning the grant-shape
finding and resolves equally to the return-path one. Merged numbers are not
being changed, since renumbering would break inbound citations to fix outbound
ones, so the convention is the fix. This file's own references use slugs for
that reason.

The same shape produced two other near-misses in the day this was written, both
worth naming because they are one class: a local view of main that is no longer
main. A filename search for a design document across this working tree missed
it because the tree was on a feature branch and the document existed only on
main. And a branch whose PR had been SQUASH-merged read as unlanded to
`git merge-base --is-ancestor`, because none of its commits is an ancestor of
main even though all of their content shipped (the finding-175 class).

Related: `finding-005-instance-ulid-collides-on-simultaneous-start`,
`finding-006-global-tier-has-no-per-agent-address`,
`finding-006-return-path-director-has-no-durable-consumer`, ArcavenAE/director
issues #62, #63 and #66.

## Addendum 2026-09-25: a live specimen after a real hub restart

Section 5 left the live question open because the discriminator (one arrival on
the global tier) never came. This is a separate seat on the same cluster where
the discriminator did arrive, and the timeline around it is the evidence.

- **Seat:** errand-supervisor-g3-0 on mokuzai, `global://mokuzai/supervisor`,
  instance `01M38DS8N1TKW5N630NNSVMHDN`, marvel
  `0.1.0-alpha.20260923.235925.97698a3`. Its director-mcp shim was started
  before the hub restart and was not restarted with it.
- **Before the restart:** sends to `global://kinu/supervisor` and
  `global://director` were refused by R-92 ("nothing is registered under
  presence.kinu." / "presence.director."), and a send to `agent://ops/michael`
  in workspace aae-orc failed with `context deadline exceeded`. Recorded
  verbatim on ArcavenAE/aae-orc#396.
- **Restart:** the operator restarted the hub at about 21:17Z after its
  presence store wedged.
- **Downward message:** director sent REQUEST `01M3D6XHX3YSM34QES5CWQDT6A` to
  `global://mokuzai/supervisor` with `sent_at` 21:18:08Z (reported by the
  operator as `GLOBAL_TO_mokuzai` seq 51).
- **Three polls, all silent:** `wait_for_message` for 60s, 120s and 120s, from
  about 21:22Z to 21:30Z. Every one returned `message: null` with
  "no message within the window; this is silence, not failure".
- **The roster said the seat was fine:** during that window the seat's global
  presence row was live (ts 21:21:23Z). This is the trap section 5 names:
  presence refreshed while nothing came down.
- **Upward worked:** a FAILURE reporting the silence went up and was accepted
  on `GLOBAL_TO_DIRECTOR` seq 114.
- **Recovery:** the operator ran `/mcp` to reconnect the shim. The first poll
  after the reconnect returned message `01M3D6XHX3YSM34QES5CWQDT6A`, the one
  published before the three silent polls. The reply went up as seq 115, and
  director confirmed seeing it at the hub.

What this establishes: a live global-tier seat returned the non-error silence
note on three consecutive polls while a message addressed to it had already
been sent, the roster showed it present the whole time, and a client-side
reconnect alone made that message arrive. That is the section 0 observable,
live rather than in the rig.

What it does not establish is the mechanism. With no hub credential this seat
could not read the consumer, so it cannot say whether the restart removed its
durable or whether the shim held a consumer handle the restarted hub no longer
honoured. The null polls rule out the local-backlog starvation of director#83
on this seat, since a backlog would have returned local messages rather than
null. That leaves the #66 shape as the likely one, and PR #82 (recreate a lost
global durable) as the change it bears on. The evidence that the message was
waiting before the polls rests on the envelope's `sent_at` and the operator's
seq, not on a pending count read at the time.

A second, unrelated delay followed in the same session and should not be
confused with this one. After the reconnect, director queued eight more
messages over about two hours (21:50Z to 00:00Z). They arrived only when the
operator prompted the seat to poll, because an idle seat does not poll. That is
the second family member in section 1, a recipient that is alive and not
polling, and a reconnect would not have helped it.

The operational rule this suggests, not yet tested beyond this one case: after
any hub restart, reconnect every shim that was running before it, and treat
silence from a pre-restart shim as unconfirmed until one message has arrived
after the restart.

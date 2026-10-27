# A shelf for the replies the operator asked for and did not see

- **Status:** idea (pre-hypothesis, no commitment). No ticket and no frontier
  node yet.
- **Date:** 2026-10-27
- **Subject:** director. The shelf would be a surface director keeps and
  publishes. The Decision Desk, the ops dashboard and the digest are objects
  it sits beside, not co-owners.
- **Related:** the ops tab design (`sim/design/ops-dashboard.md`); the Desk
  card parser (`sim/design/card-ingest.md`); the timed inbox look and its
  digest (`sim/design/timed-inbox-look.md`); the director functions redesign
  (`sim/design/director-functions-redesign.md`, row 8, operator digests).

## The operator's words

> "kos idea director we have a lot of adhoc or temporary requests, briefings, reports or information that the operator wants to review/requests and no side channel place to park a summary reply so the operator asks, doesnt get to see the streaming inline reply and has to ask later and burn tokens. seems like a good candidate for another artifact/dashboard item, with the ability to clear that item from the requests summary or to ask more info, to link to/open the full report or research or tasks, and to dispatch related work or review related recommendations"

## The idea, in one sentence

Give each answer the operator asked for, and then missed in the stream, a
parked summary on a surface the operator can return to, so that finding it
again costs a glance and not a second question.

## The problem

The operator asks a seat for a briefing, a report or a piece of information.
The reply streams inline while the operator is elsewhere, so it is not read.
Nothing holds a short version of it. The operator asks again later, and the
seat spends tokens producing what it already produced.

These requests are not decisions. They have no options to rule on, so they do
not belong on the Desk. They are not fleet state either, so they do not belong
on the ops tab. They are replies waiting to be read.

## The item shape

One item per reply, small enough to read at phone width:

- **Summary:** one paragraph, written by the seat that produced the reply.
- **Full source:** a link to the full report, research or task list the
  summary came from. The summary never replaces it.
- **Source ask:** the request that produced the item (the ask id, and who
  asked of whom), so the operator can tell what a summary answers.
- **State:** `new`, `read` or `cleared`. An item is `new` when it lands,
  `read` once the operator has opened it, and `cleared` when the operator
  dismisses it.

Two things the item does not carry: a recommendation presented as a ruling,
and any action taken on the operator's behalf. It is a reply, parked.

## What the operator can do with an item

- **Clear it.** Remove it from the shelf. A cleared item stays recoverable in
  a short history, so clearing is never a loss.
- **Ask for more.** Send a follow-up on the same source ask, so the seat that
  answered gets the context it already has.
- **Open the full source.** Follow the link to the report, research or tasks.
- **Dispatch related work.** Turn a finding in the summary into a request to a
  seat, carrying the item as its source.
- **Review related recommendations.** Open the recommendations the reply
  contained, one at a time, each with its sell-by date.

Each action is a request that director relays. None of them changes a seat's
authority, and none acts on silence.

## How it relates to what exists

Read in director's own docs on 2026-10-27.

- **The Decision Desk** holds decisions only. Its cards are parsed from a
  seat's card file into a `decisions/<slug>` document, and a card missing a
  part goes back to its seat (`card-ingest.md`). A reply with no question in
  it fails that shape by definition. Widening the card to hold replies would
  weaken the one check the Desk has.
- **The ops tab** is the second tab of the Desk page, drawn from `dash_*`
  collections that director writes on its sweep (`ops-dashboard.md`, section
  3). It answers where work sits and what waits on the operator. Its design
  rules out buttons, and only a session can write artifact data, so a
  clear action there would need a decision the design has not made. It also
  reads its own database only, so a shelf on the same page would read the
  same store.
- **The digest** is the twice-daily summary the operator reads. The functions
  list marks it as defined by habit only (`skills/director/reference/functions.md`, function
  8), and the timed inbox look already gives its digest a "cleared" list
  (`timed-inbox-look.md`). The shelf's `cleared` state would repeat that
  word with a different meaning unless the two are reconciled. A digest is
  read on a schedule, and the shelf is read when the operator comes back for a
  reply, so they differ in cadence.

## Where it could live (not decided)

- **A new panel on the ops tab.** It shares the page and the sweep that
  writes it. It conflicts with the no-buttons ruling, and the tab is about
  fleet state, where a reply is not state.
- **A lane on the Desk.** It shares the operator's existing habit of opening
  the Desk and the write path that card-ingest defines. It conflicts with the
  Desk holding decisions only, and it would put non-decisions in the list the
  operator reads for age bands of open questions.
- **A third surface.** Its own artifact with its own store. Cleanest on
  meaning, and one more page to open.

This note picks none of them.

## Open questions

- Who writes an item: the seat that produced the reply, or director on
  noticing a reply the operator did not open?
- How does director know a streamed reply went unread, rather than parking
  every reply?
- Is `read` set by opening the item, or by the operator saying so?
- How long does a cleared item stay recoverable?
- Which items should interrupt, and which wait, under the attention budget
  that R-135 describes?
- What must never go on the shelf, given that a Contributor can see the Desk
  page (`ops-dashboard.md`, section 3, on content bounds)?

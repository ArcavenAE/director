# Relay template

A relay is a message director sends on someone's behalf or about someone's
ruling. Most misses in a relay are slips under rush: a claimed authority, a
misattributed sender, text sent to the wrong owner. Filling this template
before the send is the check inside the answer. It carries meaning, not
wording: nothing here limits how the message is phrased.

Fill every field. A field that cannot be filled is a reason not to send yet.

```
To:         <the address, and the seat or file that owns the matter>
From:       <who the receiver should think this comes from: director, or the
             human named, and only if the human said it in this conversation>
Authority:  <the ruling or instruction this rests on, quoted, with who gave it
             and when. "None, this is a question" is a valid entry.>
Ask:        <one line: what the receiver should do or decide>
Context:    <the facts the receiver needs that it cannot get from the message
             alone, each with its source>
Lands in:   <where this text ends up (a pane, a file, a public thread) and who
             owns that place. Public destination: ids only, no private
             content.>
Reply by:   <a date or an event, or "no reply needed">
Log:        <the relay-log entry: `${DIRECTOR_HOME:-$HOME/.director}/bin/relay-log append --to <to> --text-file ...`>
```

## The three questions behind it

1. Authority: does this touch a standing ruling, or claim authority? Quote the
   ruling. If you cannot quote it, the message is a question, not an
   instruction, and says so.
2. Attribution: who said it, in which message? A paraphrase of a seat's report
   is not the human's word.
3. Destination: where does the text land, and does that file or seat own it? A
   message into another seat's pane or branch is a request to its owner.

Before an inject that ends in Enter, run `<checkout>/skills/director/scripts/composer-check` (see
SKILL.md, "Standing checklist").

## What this does NOT ask

- It does not forbid interpretation. Carrying meaning is the job (see
  `relay.md`); the template only makes the authority and the destination
  explicit.
- It is not a form the receiver must return. It is for the sender's own use
  before sending.

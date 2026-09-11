---
description: "Sweep agent sessions across every installed harness and report what is blocked on you"
argument-hint: "[sweep|standing|harvest] [--days N]"
---

Invoke the `director` skill.

Mode: `$1` if given, otherwise `sweep`.

In sweep mode: run the inventory, read the board, and report blocked-on-you,
stranded, running, and uncaptured. One line per item. Take no action and send
no messages.

In standing mode: adopt the director role for the remainder of the session,
including the capture triggers and the relay log. End the session with a
harvest.

In harvest mode: read the captured notes and promote what has hardened into
sim/requirements.md, applying the admission test and tagging each promoted
entry with its source class. Report promoted, unchanged, and rejected.

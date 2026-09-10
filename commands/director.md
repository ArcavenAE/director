---
description: "Sweep agent sessions across every installed harness and report what is blocked on you"
argument-hint: "[sweep|standing] [--days N]"
---

Invoke the `director` skill.

Mode: `$1` if given, otherwise `sweep`.

In sweep mode: run the inventory, read the board, and report blocked-on-you,
stranded, running, and uncaptured. One line per item. Take no action and send
no messages.

In standing mode: adopt the director role for the remainder of the session,
including the relay discipline and the capture triggers.

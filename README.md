# director

Supervisor communications and multi-agent coordination for the Dark Atelier
platform. The human's assistive agent for running work across many agent
sessions on many machines, under many harnesses.

**Status: simulation.** The software does not exist yet. What exists is a
skill that has a Claude Code session play the role by hand, plus the
instruments it uses and the requirements that fall out. This repo is the
authoritative source for both, and the requirements are the point.

## Why it exists

An operator running several agent sessions is the message bus, and that is
the defect. Sessions stop to ask a question and the question waits on one
human's attention; when the session ends first, the work it did and the
answer it needed are both lost. Observed with nine sessions on one laptop:
five had died holding unanswered questions.

Director's product is therefore **custody**, not coordination. Nothing
produced is lost and nothing asked is dropped. Message routing is downstream.

## Not made redundant by any one harness's remote control

Harness vendors are building session-to-session messaging, and it is good.
It is also single-vendor, single-account, single-machine. Director must reach
sessions running Claude Code against Bedrock or the Claude platform on AWS,
codex, crush, opencode, raw SDK streams, and whatever is next, across hosts
and accounts. The measured capability table in
`skills/director/reference/adapters.md` is the argument: of four harnesses
installed on one machine, exactly one exposes presence or an address.

There is a subtler reason too. Claude Code's own cross-session messaging does
not deliver a message, it delivers the message wrapped in a policy paragraph
the receiving harness writes, which tells the receiver the sender is "very
likely" acting for the user. That hedge is a probability estimate standing
where a signature belongs, it is not ours to version or extend, and no other
harness ships anything like it. Building director on that feature means
inheriting safety we cannot inspect and cannot carry:
`skills/director/reference/relay.md` and
`sim/specs/vendor-injected-receiver-policy.md`.

## Layout

```
skills/director/     the skill: role, modes, output contract, capture triggers
  reference/         measured capability table; the settled and open relay questions
  scripts/dsi        session inventory across all four adapters
  scripts/dsx        external state verification (the board has no expiry)
commands/director.md thin command that invokes the skill
install.sh           symlink or copy the skill and command into ~/.claude
sim/                 the simulation record
  prompt-session-1.md  the original direction, verbatim
  specs/               requirements firm enough to constrain the software
  artifacts/           work products the simulation produced
```

`sim/notes/` and the state root (`~/.director/state` by default) are
gitignored. They carry live operational detail about real sessions and are
never published; anything durable is rewritten clean into `sim/specs/` or
into the platform's knowledge graph.

## Install

```sh
./install.sh          # symlink into ~/.claude, edits go live
/director             # sweep: what is blocked on you
/director standing    # adopt the role for the session
```

## Reference clones

`multiclaude/` and `tmate/` are third-party checkouts kept locally for
reference and are not vendored. multiclaude is the store-and-forward design
predecessor; tmate is prior art for reaching a terminal session you do not
share a machine with.

## Provenance

Session 1 of the simulation ran 2026-09-04 over nine sessions and three
hours. It produced nine envelope fields, each earned by a specific observed
failure, and one unpredicted result: the coordination was not the valuable
part, the custody was. It also silently dropped five of its own messages
while every send reported success, which is why liveness is a requirement
here and not a nicety.

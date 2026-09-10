# director/sim - simulating director before building it

A Claude Code session plays the director role by hand, using whatever local
features the installed harnesses happen to expose as a stand-in for the
transport director will later own. Everything here is Mr. RightNow:
shortcuts taken knowingly, to discover what director must actually do.

The role now lives in `../skills/director/SKILL.md`, and its instruments in
`../skills/director/scripts/`. This directory is the record.

## Layers (F25 stage rig, applied to this workspace)

| File | Kind | Authored by | Rule |
|---|---|---|---|
| `$DIRECTOR_STATE/sessions.json` | prop | `dsi` | machine, verbatim, never hand-edited |
| `$DIRECTOR_STATE/roster.md` | scenery | `dsi` | machine, regenerated freely, lossy heuristics |
| `$DIRECTOR_STATE/board.md` | **backdrop** | director + human | AUTHORED. Judgment lives here. Survives compression. |
| `notes/friction.md` | prop | director | append-only; capture BEFORE workaround |
| `notes/observations.md` | prop | director | requirements discovered by simulating |
| `notes/relay-log.md` | prop | director | every message sent, verbatim, with outcome |
| `specs/` | requirement | director | firm enough to constrain the software |

`board.md` is the thing to read first after a context compression: it carries
what was concluded, not what was seen.

The state root defaults to `~/.director/state` and the notes are gitignored.
Both describe live sessions doing real work; neither is published. A
requirement worth publishing is rewritten clean into `specs/`.

## Shortcuts in use (must NOT survive into director's design)

1. `~/.claude/sessions/<pid>.json` - Claude Code's own session roster
   (sessionId, cwd, name, status, `messagingSocketPath`). Undocumented
   internal.
2. `ListAgents` / `SendMessage` - Claude Code's built-in local messaging over
   `/tmp/cc-socks/<pid>.sock`, which in session 1 silently dropped five of
   twenty messages while reporting success on every one.
3. Reading other sessions' transcripts and stores directly off disk, across
   four harnesses.

The first two work only because every session is the same user, the same
subscription, and the same machine. Director's real form has none of those.

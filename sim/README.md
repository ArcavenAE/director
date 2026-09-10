# director/sim — simulating director before building it

A Claude Code session plays the director role by hand, using Claude Code's
own local features as a stand-in for the bus director will later own.
Everything here is Mr. RightNow: shortcuts we take knowingly, to discover
what director must actually do.

## Layers (F25 stage rig, applied to this workspace)

| File | Kind | Authored by | Rule |
|---|---|---|---|
| `state/sessions.json` | prop | `bin/dsi` | machine, verbatim, never hand-edited |
| `state/roster.md` | scenery | `bin/dsi` | machine, regenerated freely, lossy heuristics |
| `state/board.md` | **backdrop** | director (me) + human | AUTHORED. Judgment lives here. Survives compression. |
| `notes/friction.md` | prop | director | append-only; capture BEFORE workaround (tooling-friction.md) |
| `notes/observations.md` | prop | director | requirements discovered by simulating |

`bin/dsi` regenerates the props. `board.md` is the thing to read first
after a context compression: it carries what I concluded, not what I saw.

## Shortcuts in use (must NOT survive into director's design)

1. `~/.claude/sessions/<pid>.json` — Claude Code's own peer roster
   (sessionId, cwd, name, status, `messagingSocketPath`). Undocumented
   internal.
2. `ListAgents` / `SendMessage` — Claude Code's built-in local peer
   messaging over `/tmp/cc-socks/<pid>.sock`.
3. `~/.claude/projects/<slug>/<sessionId>.jsonl` — reading other
   sessions' transcripts directly off disk.

All three work only because every session is the same user, same
subscription, same machine. Director's real form has none of those.

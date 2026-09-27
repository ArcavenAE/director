# finding-013: claude's folder-trust dialog opens on "No, exit", so an injected Enter ends the seat, and the launcher's guard never checks trust

- **Date:** 2026-09-27
- **Session:** arcaven-builder-g5-0, placing the 2026-09-27 seat harvest
- **Subject:** seat launch through `sim/twin/cast-launch.sh`
- **Confidence:** the dialog text observed live and recorded verbatim in a 2026-09-26 startup-error study; the guard read at 02a3188

## 0. The sentence

**Whether a seat's start directory is trusted is knowable before the start,
but the launcher only requires that a directory be named, so an untrusted one
stops the seat at a dialog whose default answer exits it.**

## 1. What was observed

claude shows this at a start in a git root (or a plain directory) it has not
been told to trust:

```
Quick safety check: Is this a project you created or one you trust? ...
❯ No, exit
  Yes, I trust this folder
Enter to confirm · Esc to cancel
```

The cursor starts on `No, exit`. A seat woken by an injected Enter (a
doorbell, a pane nudge) exits instead of starting. A plain subdirectory of a
trusted root does not show the dialog; a nested repository root inside a
trusted tree does, so a directory that later becomes a worktree or subrepo
root meets it on its next start.

## 2. Mechanism

claude records acceptance per path as `projects[<path>].hasTrustDialogAccepted`
in `~/.claude.json`. So trust is a file read away before any start.

`cast-launch.sh` (around lines 200 to 209) requires `TWIN_CWD`, with no
default, and cds to it. Its comment says the directory "must be one the
harness already trusts on this host", but nothing checks that it is. A wrong
`TWIN_CWD` fails at the dialog, after the launch has reported success.

## 3. What a preflight would do

Read `hasTrustDialogAccepted` for the git root that contains the start
directory, and refuse the launch with a message naming the path when it is
absent. Never answer the dialog for the seat: the comment's rule ("never
bypass the dialog with dangerous permissions") stands, and trust stays an
operator act.

## Related

- marvel finding-045 (the same gate from marvel's side: a fresh cast stalls at it; marvel could pre-clear it) and marvel#255 (MARVEL_WORKDIR, which retires this launcher's cd)
- marvel finding-049 (codex project trust defeats the sandbox): the codex analogue is a different gate with the opposite hazard, so it is not folded in here

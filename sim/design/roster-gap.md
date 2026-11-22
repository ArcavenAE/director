# A seat up in marvel and absent from the director roster

A seat can be running in marvel and never reach the bus: it sits at a first-run prompt, its director MCP failed to
start, or it was launched with none configured. director cannot see it, because the checks that read the roster only
see seats that joined. This adds a sweep line that compares the two lists and names each such seat. It reminds only:
it writes nothing, sends nothing and gates nothing.

## What it compares

- `marvel get sessions --all-clusters -o json`: the claude seats, that is running sessions whose runtime name or
  command names claude. That view is refused inside a seat, so the sweep runs this as the operator, or reads a file
  with `--sessions`.
- `director-mcp roster --json`: the presence rows of the local broker.

## The join key

The pair (team, name): the session's `Team` and `Name` against the roster row's `team` and `agent_id`. Both sides are
built from the same seat name, so the same name under another team does not join.

| marvel session | roster row | joined |
|---|---|---|
| Team `t1`, Name `t1-builder-g1-0` | team `t1`, agent_id `t1-builder-g1-0` | yes |
| Team `t2`, Name `t2-builder-g1-0` | team `other`, agent_id `t2-builder-g1-0` | no |
| Team `t1`, Name `t1-lost-g1-0` | no row | no |

`director-mcp roster --json` reads the local broker. `--roster FILE` applies one roster to every cluster, and
`--roster CLUSTER=FILE` gives a cluster its own. When per-cluster rosters are given and a cluster has none, that
cluster prints a GAP and its seats are not claimed missing.

## The line

One line per seat that has run more than 10 minutes (now minus `CreatedAt`) with no roster row:

    roster-gap: alpha/t1-lost-g1-0 team t1 running 180 min, no roster row, reason unknown

A session with no readable `CreatedAt` prints with `age unknown`. It is never dropped.

## Why there is no row

A seat launched with no director MCP configured never joins, so it is classed apart from a missed join. The script
tells by the seat's pane: `$ROSTER_GAP_PANE` names a program called with the cluster and the session name that prints
the pane text. The text `no MCP server configured with that name` (as seen on a bootstrap seat) gives `no director MCP
configured`. A pane read without that text gives `no roster row`, a missed join. With no program, or a pane that cannot
be read, the reason is `reason unknown` and the seat is still printed. The session record carries no MCP setting, so
the pane is the only evidence the script uses.

## Gaps

`roster-gap: GAP ...` is printed, never zero seats, when a cluster's record is not answered, starting or same_daemon;
when the sessions or the roster cannot be read; and when a cluster has no roster among per-cluster ones. The last line
counts the seats checked, the seats without a row and the cluster gaps.

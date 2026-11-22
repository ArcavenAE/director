# Login expiry warning

A harness login lapses on a date the harness knows and nobody else reads. When it lapses, every seat on that host and
HOME sits at a login prompt until someone notices. This adds a daily check that reads the expiry and says so 72 hours
ahead. It reminds only: it sends nothing, restarts nothing and gates nothing.

## What it reads

`claude auth status --json`, keeping three keys: `loginExpiresAt`, `loggedIn` and `subscriptionType`. The command also
prints an email address and an organization. The script parses the answer in memory and keeps only those three keys, so
neither value is printed, logged or written. A failed read prints that the answer was unreadable and nothing from it.

## What it prints

- 72 hours or less before `loginExpiresAt`, or `loggedIn` false: a Desk-ready line (host, HOME label, expiry, hours left,
  subscription) and an introspect line in the introspector's `| id: | remedy: | owner:` form.
- More than 72 hours: nothing to the operator, and one line in `state.log`.
- An unreadable answer: a Desk line that says UNKNOWN, since silence is the failure this exists to remove.

A HOME is named by a label (`--home-label`, default `default`), never by its path.

## Renewal

`last-expiry.json` keeps the last seen expiry per host and HOME label. When the new expiry is later, the script reads
`marvel get sessions --all-clusters -o json` and prints one line per running seat whose harness state is `logged-out`:
`renewal: nudge CLUSTER/SEAT (logged-out)`. That view is refused inside a seat (`MARVEL_SESSION` set), so the renewal
listing can only run as the operator, for example from a launchd entry. When the sessions cannot be read in full, that
is, marvel gives no answer or a cluster is not `answered` or `same_daemon` (a `starting` cluster included, since its rows
may be incomplete), it prints the seats it did see, then `renewal: GAP (...)` naming the cluster, and leaves the stored
expiry, so the next run lists again. A seat may be nudged twice in that case. marvel's exit status alone is a GAP only
when no cluster explains it.

## State

`$LOGIN_EXPIRY_STATE`, else `$DIRECTOR_STATE/login-expiry`, else `~/.director/state/login-expiry`: directory mode 700,
files mode 600.

## Schedule (proposed, not installed)

Once a day per host and per HOME, early enough that 72 hours still leaves a working day. A launchd calendar entry per
HOME with `--home-label` set runs the warning and the renewal listing. A marvel scheduled role could run the warning
and send its lines to director, but it runs as a seat, so the renewal listing would print a GAP there and the stored
expiry would never advance; only the launchd entry lists. Installing either is the operator's call.

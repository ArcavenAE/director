# Reply shelf

The operator asks a seat for a report, the reply streams past while they are elsewhere, and a second ask burns
the tokens again. The shelf parks a one-paragraph summary of each relayed answer, with a pointer to the full
source, where the operator can read it later, clear it, or ask for more. This is the design the idea file
(`_kos/ideas/operator-reply-shelf.md` in the orchestrator) leaves open; the rulings below are director's, 2026-11-09.

## Rulings

- **A separate artifact**, with its own store, not a tab on the Desk. The Desk's db rule covers `decisions`
  and `answers`; the shelf has its own collection.
- **14-day recovery.** A cleared row stays in the collection and shows under "Cleared, recoverable" for 14
  days from `cleared_at`; "Recover" sets it back to `read`. After 14 days the page stops showing it; the
  document is not deleted by the page.
- **Read by opening.** A row is `new` when it lands and `read` once the operator opens it.
- **Source** is an https URL, a repo path, or `<host>:<absolute path>` with the file's `sha256`, since most
  answer sources are host-local report files. A host name is fine: the page is private.
- **Seed rows** are written by director itself from the answers it relayed. The repo carries the seed
  format and a fixture with placeholder ids, never real rows.
- Dispatch and Review stay out (phase 2).

## The page

`skills/director/shelf/shelf.html`, title "Director Reply Shelf" (the install's page namespace is added at
publish, as for the other pages). Capabilities:
`{"db":{"rules":[{"path":"replies","write":"owner"}]},"user":{}}`. Phone-width single column, newest first,
state shown as text. Row text is drawn as escaped text: no Markdown, no link, no image, no network call.

The page writes only the operator's own fields, each as a patch built in one logic block
(`/*shelf:start*/` to `/*shelf:end*/`) that `verify-shelf-page.sh` runs:

| action | patch |
|---|---|
| open a `new` row | `{state: "read", read_at}` |
| Clear | `{state: "cleared", cleared_at}` |
| Recover | `{state: "read", cleared_at: ""}` |
| Ask for more | `{more, more_at}` (text trimmed, at most 2000 characters) |

Nothing is sent anywhere by the page: a page cannot reach the bus. Director reads `more` on its sweep and
relays it on the source ask.

## The row

One shared database on the artifact, collection `replies`, document id `r-<yyyymmdd>-<n>`.

| field | content |
|---|---|
| `summary` | one paragraph, at most 1200 characters |
| `source` | https URL, repo path, or `<host>:<absolute path>` |
| `sha256` | the file's hash; present only with the host form |
| `ask` | the intake id, `I-<n>` |
| `asked_by`, `asked_of` | seat or role names |
| `state` | `new`, `read` or `cleared` |
| `created_at`, `read_at`, `cleared_at` | UTC times, empty until set |
| `more`, `more_at` | what the operator typed under "Ask for more"; empty otherwise |

## Content checks

A Contributor can see the page, so `shelf-row` bounds what a row may hold, with the dash writer's list
(`dash-withhold.txt`, 0600, absent or wider refuses every row):

- the summary has no token shape, private key header, `password=`-style assignment, bearer value, credential
  URL or name from the private-names list, and is one paragraph;
- `source` is a form above, with no credentials, no `..`, and no private name; a bare local path is not a source;
- host names and absolute paths in a summary pass. The dash rows refuse them; the shelf does not, by the
  2026-11-09 ruling.

A refusal prints one fixed line, `shelf-row: refused: <field>:<reason>`, and never the value.

## How rows get written

`shelf-row new ...` builds and prints one `set` write; `shelf-row seed FILE [--start N]` prints a batch of at
most 50 from a seed file `{"rows": [{ask, asked_by, asked_of, summary, source[, sha256]}]}`. A script cannot
write artifact data (finding-director-glst), so a director session applies the output with ArtifactData. The
sweep also lists rows whose `more` is non-empty and relays each on its source ask.

## Proof

`verify-shelf-page.sh` (the logic block, the declared capabilities, no raw row text, no network) and
`verify-shelf-row.sh` (the row shape, every refusal, the seed batch, nothing written), both with a mutant that
turns each check red.

# `director-mcp roster --json`: the output contract

The command lists the presence rows of the local broker so a script can build a
seat table without reading the broker's storage. Other programs read it (marvel's
seat-gap check is one), so its shape is a published interface. It reads only: it
creates no consumer and writes no presence.

## Shape

```json
{
  "schema_version": 1,
  "broker": {"url": "nats://127.0.0.1:4222", "server_name": "hub-1"},
  "read_at": "2026-11-23T17:00:00Z",
  "rows": [
    {"agent_id": "crew-builder-g1-0", "team": "crew", "role": "builder",
     "instance": "01AAA", "age_seconds": 12.5, "fresh": true}
  ],
  "warnings": []
}
```

- `schema_version`: an integer, 1 today. It changes only when a reader of the old
  shape would misread the new one. A field added beside the others does not
  change it, so a reader ignores fields it does not know and refuses a version
  it does not know.
- `broker.url`: the address the command read, with any user or password removed.
  `broker.server_name`: the connected server's own name, omitted when the server
  reports none. A host with more than one cluster uses these two to tell whose
  rows it has read.
- `rows`: one per presence row. Each carries `agent_id`, `team`, `role`,
  `instance`, `age_seconds` and `fresh` (no older than the presence bucket's own
  bound), and may carry `workspace`, `state`, `ts`, `connected_at`, `polled_at`,
  `waiting` and the cue fields. A row whose record cannot be read stays in the
  list with `fresh` false and a line in `warnings`; it is never dropped.
- `warnings`: omitted when empty.

## Exit status

| status | meaning | stdout |
|---|---|---|
| 0 | the roster was read | the document above |
| 1 | a broker answered and the read failed (no presence bucket, a record error), or the address did not parse | nothing |
| 2 | the arguments were refused | nothing |
| 3 | no broker reachable at the address | nothing |

A read that failed once a broker answered is still exit status 1, as it was.
A caller that must say "director is not present" and not "unreadable" tests for
exit status 3. Before this contract a refused connection also exited 1; a caller
that treated every nonzero status alike is unaffected.

## Changing it

A change to the shape bumps `schema_version` when an existing reader would
misread it, and updates this file and the tests in
`cmd/director-mcp/rostercmd_contract_test.go` in the same pull request.

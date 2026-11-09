# director recipes

A library of launch recipes that director owns, in the shape marvel's company and
recipe schema defines. It holds one recipe, `bus-seat`, which carries as data what
`sim/twin/cast-launch.sh` decides today for a seat that talks on the director bus.
Nothing here changes a running seat: marvel does not read it until a team pins it.

## Tree

```
recipes/                  the library root: the directory an installer takes
  company.toml            the library's index
  switch-list.toml        data beside the index, not read as a recipe
  parity-table.toml       data beside the index, not read as a recipe
  recipes/                the schema's own directory for recipe files
    bus-seat.toml         the one recipe
```

`recipes/recipes/` is not a mistake. The library root is named `recipes/` in this
repository, and marvel's schema keeps its recipe files in a `recipes/` directory
under the library root. Leave both names alone.

## Layout

| file | what it is |
|---|---|
| `company.toml` | the library's index: name, kind `recipes`, schema, the marvel floor, the definition schema its recipes expect. It carries no `version` and no `source`; see Stamping |
| `recipes/bus-seat.toml` | the bus-seat recipe |
| `switch-list.toml` | which bus roles may move to the recipe; the director seat and research-supervisor stay on `cast-launch.sh` until marvel admits them, and the supervisor stays there until the supervisor cue default is decided (aae-orc-no9i4.4) |
| `parity-table.toml` | cases 4 and 6 of `sim/twin/verify-cast-launch.sh` as data, with the one relaxation (`DIRECTOR_CLUSTER`) recorded in the file |

marvel strict-decodes every file in `recipes/recipes/` as a recipe, so only recipes go
there. The switch list and the parity table sit beside `company.toml`.

## What the bus-seat recipe carries, and does not

It carries the seat's role (`DIRECTOR_ROLE`), the session name (`-n {session}`), the
director MCP server with `strict = true`, the pre-flight (`director-mcp --preflight`,
no timeout: marvel's default and ceiling apply), a `cue` parameter, and a
`when.global.non_holder` block that unsets `DIRECTOR_GLOBAL_DOMAIN` and
`DIRECTOR_GLOBAL_ROLE`. `DIRECTOR_CLUSTER` is not unset: marvel constructs it and the
shim ignores it when no domain is set.

It carries no `when.global.holder` block and no env that names `DIRECTOR_GLOBAL_DOMAIN`,
so a holder's domain has one source, its role environment. It carries no
`DIRECTOR_BUS_REQUIRED`, no Bedrock command, no bus credential name and no
`DIRECTOR_NATS_*` or `MARVEL_BUS_STATE` entry.

## Checks

```
scripts/verify-recipes.sh          the structural checks, each shown to fail on a fixture
scripts/recipes_lint.py recipes    the checks alone, over a library directory
sim/twin/parity-table.sh           the parity table against cast-launch.sh (CAST_LAUNCH names another launcher)
```

CI runs `verify-recipes.sh`. These are structural checks over a data file, so they may
gate a merge.

## Stamping

The committed `company.toml` cannot hold the commit it is built from. The installable
form is a stamped copy:

```
scripts/stamp-recipes.sh recipes <out-dir>
```

It writes `version = "<commit date>-<short sha>"` and `source = { url, commit }` into
the copy and leaves the committed file alone. It refuses a library with uncommitted
changes, and an out-dir that is the library, inside it, a parent of it or of the
repository, or not empty. It removes nothing.

Cutting a release is a separate act and is not part of a pull request: tag
`director-recipes-<version>` and publish the git tree id with the tag. An installer that
takes a directory (`--from <dir>`) installs the stamped copy until a git install exists.

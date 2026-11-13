# Hub user lifecycle: one director command to add, list, rotate and revoke

Status: draft design, architect seat, 2026-11-13. Proposed home:
`sim/design/hub-user-lifecycle.md`. Read at director main fa262a2.
Recommendations are valid until 2026-11-27; the architect seat that wrote
this re-checks them then.

Every deployment of director runs a global hub, and over that hub's life its
users change: a leaf per cluster, director's own connection, the read-only
observer (I-469), and later majordomo and a third reader. Today each change is
a hand edit of the hub conf, a key made with whichever NATS tool is on the
host, and a reload, so every deployment re-learns the steps and gets them wrong
in its own way. This design makes the lifecycle a director feature: one
command, keys made by director itself, a grant taken from one template per
user class, and a rollback that runs on failure. Additive: nothing existing
changes until a deployment adopts its users.

## 1. The command

`director hub user <verb>`, implemented in the director-mcp binary as a new
mode beside `unread` and `roster`. That binary already links nats.go and
nkeys, has a test harness that starts a real `nats-server`
(observer_test.go `startObserverHub`), and is the one artifact
director-install builds. `scripts/director` dispatches `hub` to it.

| verb | does | exit |
|---|---|---|
| `add NAME --class C [--param k=v]... [--public-key U...]` | registers a user, mints its key unless one is given, renders, validates, reloads, verifies | 0 done; 1 refused or rolled back; 2 usage |
| `list [--json]` | the registry, joined with the live conf and the hub's monitor endpoint: class, public key, created, rotated, connected now, and any drift between registry and conf | always 0 (diagnostic) unless usage |
| `rotate NAME` then `rotate --finish NAME` | adds a second key with the same grant, so the holder can switch with no outage; `--finish` removes the old key | as add |
| `revoke NAME` | removes the user, reloads, verifies the old key is refused, and moves its seed to `keys/revoked/` | as add |
| `adopt PUBLIC_KEY --name NAME --class C` | records a hand-written user in the registry as `adopted` and prints any grant difference between the hand block and the class template. It changes no conf bytes. Re-rendering an adopted user into the managed region belongs to `rotate`, because a changed grant reaches an established leaf only after that leaf reconnects (finding-009) | as add |
| `settings init [--set NAME=VALUE]...` | once per hub: adds the settings include and the empty managed region to the conf, and writes settings.conf (section 4a) | as add; refuses a second run |

The verbs that change things share one sequence:
1. Take a lock on the hub home: `$DIRECTOR_GLOBAL_HOME/.hub-user.lock`. The
   name is a contract, so any other tool that edits the hub conf takes the same
   lock.
2. Render the candidate conf to a temp file.
3. Run `nats-server -t` on it. A candidate that fails stops here, and nothing
   has been written.
4. Copy the live conf to a timestamped backup.
5. Rename the candidate over the live conf.
6. Reload: SIGHUP to the pid in the hub's pid file.
7. Verify the change.

If any later step fails, it restores the backup, reloads again, and prints what it
did and what state the hub is in now. These are the I-469 script's steps, as
code with tests.

Verify by class:
- For a minted key, it connects as the new user. It runs the class's positive
  reads and the class's harmless negatives (requests that cannot take mail even
  if wrongly allowed, such as `MSG.NEXT` on a durable that does not exist). For
  the observer, it also checks that each stream's consumer count is unchanged.
- For a key given by `--public-key` (a leaf whose seed stays on its own host),
  it checks the user is in the reloaded conf. `list` then shows the leaf as
  connected once the leaf attaches.

Before any change, the command runs `nats-server -t` on the live conf exactly as
it stands. If that fails, it changes nothing and says the check cannot see the
hub's own configuration (section 4a). Without this control, a failure after an
edit cannot be told apart from a check that runs with less than the server has.
The first live run of the I-469 script hit exactly that: its candidate conf
failed `-t` on a variable the running hub gets from its service manager. The
script's rollback restored the conf byte for byte. The control would have
stopped the run before the backup and the key.

The command touches only one region of the conf, between `# BEGIN director
managed users` and `# END director managed users`, inside the account's
`users` list. Users outside the region are left alone, so a hand-written
break-glass entry survives. This extends the BEGIN and END pattern the
observer template already uses.

## 2. User classes and their one template each

The grants live in the repo as one template per class, at
`cmd/director-mcp/hubclasses/<class>.conf`, embedded in the binary with
`go:embed`. A release install therefore carries them without a checkout. The
command renders them with named parameters only:

| class | parameters | grant source today |
|---|---|---|
| `leaf` | `cluster` | the per-cluster leaf blocks in the hub conf template, plus `global.*.supervisor.inbox` in publish: supervisors publish to each other's inbox across clusters without the director (global-bus-tier.md 4.1, ruled 2026-09-27) |
| `leaf-director` | `cluster` | the leaf whose broker also carries the director seat (one per deployment): the `leaf` grants plus `global.director.>` in publish and subscribe and the JetStream API for `GLOBAL_TO_DIRECTOR`, with message delete on that stream only |
| `director` | none | the "director seat connecting directly" block |
| `observer` | `holder` (director, majordomo, or a later reader), giving the inbox prefix `_INBOX_observer_<holder>` | the `observer.director` BEGIN/END block |
| `admin` | none | the provisioning and break-glass user |

Reserved holders (`observer.majordomo` and the third reader) are rendered as
commented lines with no key. Issuing one is an `add`, run when the operator
rules it. `revoke` refuses to remove the last admin. One template per class
means a grant fix is one edit, and `list` reports which users carry an older
grant until they are re-rendered.

The registry is `$DIRECTOR_GLOBAL_HOME/users.json`, written by temp file and
rename. It holds name, class, parameters, public key, state and timestamps,
and never a seed. The conf region is always rendered from it, so the two
cannot drift except by a hand edit, which `list` reports.

## 3. Keys

Director makes keys with `nkeys.CreateUser()` (nkeys v0.4.16, already in
cmd/director-mcp/go.mod and used by observer_test.go). There is no dependence
on `nk`, on a `nats` CLI version, or on `nats auth nkey gen` (which start.sh's
header names today).

- The seed is written with O_EXCL to a temp file, mode 0600, in the 0700 keys
  directory, then renamed. It is never printed, logged or put in an
  environment variable. Only the public key is printed.
- Where the key is used on another host, as with a leaf, the default is that
  the key is born there and only its public key comes to the hub
  (`add --public-key`). `director hub user keygen NAME` runs on that host and
  writes the seed locally. This is the same rule hub-csr.sh follows for the TLS
  key. Nothing in the command copies a seed between hosts.
- Revoked seeds are moved, never deleted, to `keys/revoked/<name>-<time>.nk`.

Custody (SOUL section 3, ADR-009): this is issuance. The deployment's own hub
mints and revokes every key, and no key means anything at a third party.

## 4. Any deployment

Every location is a documented setting with a default under the user's home.
No host name, account name or fleet path appears in the code or the templates.

| setting | default | meaning |
|---|---|---|
| `DIRECTOR_GLOBAL_HOME` | `~/.director/nats-global` | hub conf, `keys/`, pid file, `log/`, registry |
| `DIRECTOR_HUB_ADMIN_URL` | `nats://127.0.0.1:4242` | where the command reaches the hub as admin |
| `DIRECTOR_HUB_CA` | unset | CA file when the client port has TLS |
| `DIRECTOR_HUB_ADMIN_NKEY` | `$DIRECTOR_GLOBAL_HOME/keys/admin.nk` | admin seed |
| `DIRECTOR_HUB_MONITOR_URL` | `http://127.0.0.1:8242` | for `list`'s connected column |

### 4a. Values the conf reads from the environment

A hub conf may reference variables (`$NAME`) that the running server resolves
from the environment its service manager or start script gave it. A config
check run from a shell does not have that environment, so it fails, or worse,
resolves a different value. Two ways to make the check see what the server
sees:

- (A) the command reconstructs the hub's start environment from a documented
  source per service manager;
- (B) every value the conf references lives in one settings file inside the
  hub home, `$DIRECTOR_GLOBAL_HOME/settings.conf`. The main conf includes it
  at the top, and it defines each as a conf variable (`NAME: "value"`).
  Measured on nats-server v2.14.6 (PR 1): with a name defined in both places,
  the conf variable wins over the environment, for the server and for `-t`.
  Also measured: a variable that settings.conf defines and the conf does not
  reference stops both the server and `-t` with `unknown field "NAME"`. So
  settings.conf may hold only names the conf references.

Recommended: B.
- The server and the check then read the same bytes, whatever starts the
  server: launchd, systemd, start.sh, a container, or something not yet
  written. A needs one reader per service manager and still misses a value
  injected at run time.
- The file sits in the hub home, under the command's lock and backup.
- A deployment's specifics (its advertise address, its store path) leave the
  shipped template and become data the deployment owns.

The cost is a one-time move for an existing hub, made by `director hub
settings init --set NAME=VALUE ...`. It runs the shared sequence: render the
conf with the include line and the empty managed region, and write
settings.conf with O_EXCL and mode 0600. It runs `-t` on that candidate before
anything is written, then backs up, renames and reloads. It checks after the
reload that the server answers and the same leaves are connected, and restores
on failure. It refuses a name the conf does not reference, and it refuses a
second run.

`settings init` moves values; it never changes one. Before any write, it
compares each `--set` value whose running value the server reports on its
monitor endpoint (`/varz`; `server_name` is the case in hand) with that running
value. A difference is refused up front, naming the setting and both values.
It also refuses when the monitor endpoint cannot be read for such a setting,
since the comparison cannot be made. Changing a hub's name is an identity
change for its leaves and its JetStream domain, a separate deliberate act and
not a settings move. Restore after a failed reload stays as the backstop, not
the designed path. This holds whether or not a given nats-server version
accepts the change on reload. `director hub settings check` lists every `$NAME` the conf
references, whether settings.conf defines it, and any name settings.conf
holds that the conf does not reference. The changing verbs refuse while any
reference is undefined, naming each one. The
operator supplies the values once. start.sh's own export of the store path
becomes a settings entry the same way. The one exception is a name the hub's
launcher supplies at each start (an address it works out from the network, for
example), covered next.

A name the launcher supplies stays with the launcher. A value in settings.conf
beats the environment, for the server and for `-t` (measured on 2.14.6), so
moving such a name into settings.conf pins it, and the hub keeps the old value
after the launcher computes a new one. `settings check` and `settings init`
therefore read the running hub's environment from the pid-file process, behind
the same guard that reload uses (the pid must run `nats-server -c` with this
conf), with `ps eww`. `check` prints `launcher NAME` for a referenced name the
hub's environment supplies and settings.conf does not define, and warns
`SHADOWED NAME` when settings.conf defines a name the environment also
supplies. `init --env NAME` leaves such a name out of settings.conf; the value
comes from the running hub, or from `--env NAME=VALUE` when no hub environment
can be read, and is used for the `-t` runs only. When the hub's environment can be read and does not hold the name, the name is not one the launcher supplies, and `--env NAME=VALUE` is refused with a pointer to `--set`. It is passed byte for byte: an
environment value may carry its own quotes, which nats-server reads as conf
syntax. A given value that differs from the hub's is refused. Every changing
verb runs its `-t` with the launcher's values added. A value with a space in
it is not read correctly from `ps eww`, so such a name is given with
`--env NAME=VALUE`.

It runs on the hub host because it signals the server and edits its conf. It
needs `nats-server` on PATH for `-t`, and that version is checked (section 6).
It works the same on macOS and Linux: SIGHUP, a pid file, and file renames.

## 5. The I-469 at-host script

Superseded. After this ships, the observer on an existing hub is:

    director hub settings init --set <each referenced NAME>=<its value>
    director hub user adopt <each existing public key> --name ... --class ...
    director hub user add observer.director --class observer --param holder=director

Each step is one command with its own rollback, and no step needs a hand
edit of the conf.

The script stays in the I-469 record as the reference sequence and is not run.
The operator's apply on the existing hub waits on this command, as ruled.

## 6. What it replaces or joins, and a new user's first hub

| today | after |
|---|---|
| hub conf template: users pasted in by hand, with the operator's own hosts and real public keys in the shipped file (probe/nats-global-tier/hub/nats-server.conf:23-120) | a template with the settings include, an empty managed region and no deployment's keys; classes embedded from `cmd/director-mcp/hubclasses/` |
| start.sh header: "generated with: nats auth nkey gen user", "pasted into nats-server.conf" (start.sh:2-4) | start.sh refuses a conf with no managed region and points at `director hub user add` |
| provision.sh: default cluster list is two of the operator's hosts (provision.sh:50) | the stream list comes from the registry's leaf users, so a new cluster's stream follows its `add` |
| CEREMONY.md, ca-ceremony.sh, hub-csr.sh, tls-mint.sh | unchanged: TLS is a separate lifecycle. The first-hub guide says when each applies |
| I-469 script | superseded (section 5) |

Gaps for a new user's first hub, in the order they stop someone:
1. The hub lives under `probe/` and no installer installs it. Neither
   install.sh nor director-install copies start.sh, provision.sh or the conf
   template. A new user has no supported path to a hub.
2. Getting the shim needs Go 1.26.5. director-install builds it from source
   and checks only that `go` is on PATH (director-install:186); the build pins
   `GOTOOLCHAIN=go1.26.5` (build-director-mcp:19), which fetches a toolchain
   over the network. There is no release download (director-install:34). This
   command lives in that binary, so it inherits the gap.
3. The prerequisites name Homebrew only and omit tools the scripts need
   (docs/getting-started.md:33-37: nats-server 2.11+, the nats CLI and Go are
   listed; python3 3.11+, jq, git and gh are not).
4. There is no first-hub guide: how to choose the deployment's hub domain,
   ports and TLS posture, then start, provision and add the first users.
5. No `doctor` check names these. `director-init --check` covers profile drift
   only. The standing-loop design (director#601, slice 9) proposes `director
   doctor`; hub rows belong there: server version, managed region present,
   registry and conf agree, pid file live.

## Limits as built in PR 1

- The leaf drop in `TestHubSettingsInitRestoresWhenTheReloadDropsALeaf` is
  simulated, not a real leaf disconnecting.
- A server_name change on reload, measured on 2.14.6 with a settings.conf
  include: `/varz` kept the old name and the log read `[ERR] Failed to reload
  server configuration: config reload not supported for ServerName: old=one,
  new=two`. Init therefore refuses such a value before any write.
- A reload is proved by the monitor's `config_load_time` moving after the
  signal, not by the hub answering: nats-server keeps the old config when it
  refuses a reload (a changed jetstream `store_dir` logs `config reload not
  supported for jetstream storage directory`), and on 2.14.6 `config_load_time`
  stayed put for that refusal and moved for a good reload. A refused reload
  restores the conf. The pid file is trusted only when `ps` shows
  `nats-server -c` with this conf.
- A value that is all digits, with no leading zero and at most 18 digits, is
  written to settings.conf without quotes, because nats-server rejects a quoted
  string where it expects a number (a port, or a size limit above the int32
  range such as 1073741824). Anything else stays quoted: a leading zero changes
  the number when it is read bare, and 19 or more digits can overflow an int64.
- nats-server 2.15.0 is unmeasured: the precedence and unknown-field
  findings and the reload behavior are measured on 2.14.6 only.

## 7. Red tests a builder writes first

All run against a scratch `nats-server` started by the test, never a live
hub, under the four run conditions of relay-run-conditions.md:
- `TestHubUserAddMintsASeedThatIsNeverPrinted`: stdout and stderr hold the
  public key and no seed prefix; the seed file is 0600 in a 0700 directory.
- `TestHubUserAddRendersOnlyTheManagedRegion`: bytes outside the BEGIN and
  END lines are unchanged.
- `TestHubUserAddWritesNothingWhenTheCandidateFailsTheServerTest`: live
  conf and registry byte-equal to before; no backup and no seed exist; exit 1.
- `TestHubUserAddRollsBackWhenVerifyFails`: a template made wrong on purpose
  is restored and reloaded.
- `TestHubUserObserverVerifyLeavesConsumerCountsUnchanged`.
- `TestHubUserRotateKeepsTheOldKeyWorkingUntilFinish`, then
  `TestHubUserRotateFinishRefusesTheOldKey`.
- `TestHubUserRevokeRefusesTheOldKeyAfterReload`: also measures whether a
  live connection of the removed user is closed by the reload. Not yet
  measured; the result goes in the doc.
- `TestHubUserRevokeRefusesTheLastAdmin`.
- `TestHubUserAdoptReportsGrantDifferences`.
- `TestHubUserUnknownVerbExitsTwoAndStartsNoShim`: director-mcp ignores
  unknown arguments today, and a typo must never start a shim that joins the
  bus.
- `TestHubUserListReportsRegistryConfDrift`, exit 0.
- `TestHubUserRefusesWhenTheUnchangedConfFailsTheServerTest`: a scratch
  conf referencing a variable that is absent from the test's environment;
  nothing is backed up, minted or written, and the message names the
  variable.
- `TestHubConfVariableResolvesFromTheSettingsInclude`: the same reference,
  defined only in settings.conf, passes `-t` and serves with that value; with
  a different value in the environment, the settings value wins.
- `TestHubSettingsCheckListsUndefinedAndUnreferencedNames`.
- `TestHubSettingsInitRefusesAServerNameThatDiffersFromTheRunningOne`:
  nothing is written, and the message names both values.
- `TestHubSettingsInitRefusesAnUnreferencedName` and
  `TestHubSettingsInitRefusesASecondRun`.
- `TestHubSettingsInitRestoresWhenTheReloadDropsALeaf`.
- `TestHubUserAdoptChangesNoConfBytes`.
- The scratch-hub harness gains an env-referenced conf as a standing case.
  The observer harness passed while the live hub failed, because its conf had
  no references.
- `TestHubConfTemplateCarriesNoDeploymentKeys`: no `nkey: U` line outside
  comments in the shipped template.

## Recommendation

Valid until 2026-11-27. Build in this order, each one PR:
1. The command with `add`, `list`, `adopt` (record only), `settings init`
   and `settings check`, the unchanged-conf control, settings.conf, and the
   embedded class templates.
2. `rotate` and `revoke`, including re-rendering adopted users.
3. The template cleanup and start.sh and provision.sh joining the registry.
4. The first-hub guide, with the hub moved from `probe/` into the install
   path.

The existing hub's observer apply waits on item 1. A release download of
director-mcp (gap 2) is separate work, and it decides how a user without Go
gets this command at all.

# Leaf-fabric probe (design brief 11, P0 to P7b)

Scratch brokers only. `rig.sh` starts a hub (domain `ghub`) and two leaf
clusters (`pa`, `pb`) on random ports above 20000 with their own store dirs
under `FAB_HOME`, and refuses the live fleet ports. `fabtool` takes the server
URL on every call, has no default, and refuses the same live ports unless
`FABTOOL_LIVE=1` is set, the deliberate opt-in for the section 5.1 cutover.
`p7-cutover.sh` and `p7b-parked-source.sh` refuse the live ports and any server
that already holds their streams.

```sh
(cd fabtool && go build -o "$SCRATCH/fabtool" .)
FAB_HOME=$SCRATCH/fab rig.sh up
FAB_HOME=$SCRATCH/fab FABTOOL=$SCRATCH/fabtool p2-linkcut.sh    # 10-minute outage by default; OUTAGE=<s>
FAB_HOME=$SCRATCH/fab p4-presence.sh
P7_URL=<scratch server> FABTOOL=$SCRATCH/fabtool p7-cutover.sh
P7_URL=<another empty scratch server> p7b-parked-source.sh
FAB_HOME=$SCRATCH/fab rig.sh down
```

P3 (`fabtool mint` plus `fabtool perm`), P5 (`fabtool sweep`) and P7
(`p7-cutover.sh`, using `fabtool unread`, `migrate`, `rollback`) ran on their
own standalone scratch servers. Subject root is `agent.<cluster>.` (ruled
2026-09-24); `out.<dest>.` is the internal outbox subject. Results:
`_kos/findings/finding-008-leaf-fabric-probe.md`.

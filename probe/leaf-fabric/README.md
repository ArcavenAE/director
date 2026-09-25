# Leaf-fabric probe (design brief 11, P0 to P6)

Scratch brokers only. `rig.sh` starts a hub (domain `ghub`) and two leaf
clusters (`pa`, `pb`) on random ports above 20000 with their own store dirs
under `FAB_HOME`, and refuses the live fleet ports. `fabtool` takes the server
URL on every call and has no default.

```sh
(cd fabtool && go build -o "$SCRATCH/fabtool" .)
FAB_HOME=$SCRATCH/fab rig.sh up
FAB_HOME=$SCRATCH/fab FABTOOL=$SCRATCH/fabtool p2-linkcut.sh    # 10-minute outage by default; OUTAGE=<s>
FAB_HOME=$SCRATCH/fab p4-presence.sh
P7_URL=<scratch server> FABTOOL=$SCRATCH/fabtool p7-cutover.sh
FAB_HOME=$SCRATCH/fab rig.sh down
```

P3 (`fabtool mint` plus `fabtool perm`), P5 (`fabtool sweep`) and P7
(`p7-cutover.sh`, using `fabtool unread`, `migrate`, `rollback`) ran on their
own standalone scratch servers. Subject root is `agent.<cluster>.` (ruled
2026-09-24); `out.<dest>.` is the internal outbox subject. Results:
`_kos/findings/finding-008-leaf-fabric-probe.md`.

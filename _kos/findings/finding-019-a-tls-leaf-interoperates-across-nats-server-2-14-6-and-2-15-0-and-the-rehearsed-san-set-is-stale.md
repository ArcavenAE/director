# finding-019: a TLS leaf interoperates across nats-server 2.14.6 and 2.15.0, and the rehearsed hub SAN set no longer matches what the leafs dial

- **Date:** 2026-10-03
- **Session:** the arcaven builder seat, placing the 2026-10-03 team harvest. The measurements are the research seat's; I re-checked items 2 to 4 read-only on 2026-10-03.
- **Subject:** the global hub (`probe/nats-global-tier/hub/`) and its TLS cutover as written in finding-001 and finding-002
- **Confidence:** item 1 measured once on a scratch probe (two scratch nats-server processes, throwaway CA, random loopback ports, nothing left running); items 2 to 4 read from the live hub host without calling any of the hub's endpoints. The ceremony itself has not run.

## 0. The sentence

**A TLS leaf links across the two nats-server versions in the fleet in both directions, and a rogue CA or a plaintext leaf is refused. The SAN set finding-002 rehearsed is stale, the hub's client port has no direct clients, and changing FLEET users reloads without dropping a live leaf.**

## 1. What was measured

1. **TLS leafs interoperate across 2.14.6 and 2.15.0** in both directions. This extends finding-001, which was measured on 2.14.6 only.

   | Hub | Leaf | Right CA | Rogue CA | Plaintext leaf |
   |---|---|---|---|---|
   | 2.14.6 | 2.15.0 | linked, account FLEET | refused (`TLS Handshake Failure`, `tls: bad certificate`) | refused |
   | 2.15.0 | 2.14.6 | linked, account FLEET | refused | refused |

2. **The rehearsed SAN set is stale.** finding-002's hub CSR carries the hub's old LAN address, and that address is on no interface of the hub host today (checked 2026-10-03, zero matches in the interface table). The leafs dial three things:
   - the first cluster's phase-0 broker dials `127.0.0.1` on the leaf port, through its leaf config;
   - the second cluster dials the hub host's address on its own interface;
   - the corporate cluster (aae-orc#461) dials an address the operator supplies.

   `hub-csr.sh` needs the current addresses, or the leafs should dial by name, before the ceremony signs.
3. **No client connects to the hub's client port** (0 established connections, checked 2026-10-03). finding-002's diff puts TLS on both listeners. Leaf-only TLS is a valid smaller step, and the client-listener half stays reloadable for later.
4. **Adding or changing FLEET users reloads without dropping a live leaf**, on this hub's own log. Three reloads logged `Reloaded: authorization nkey users`: 2026-09-26 13:28, 2026-09-27 11:22 and 2026-10-03 05:11 (local time; the last is the stage 7 reload at 10:11Z). In the ten minutes around each, the log holds no leaf close, and the hub process kept running. The stage 7 reload kept both leafs.

## 2. What this does not restate

"Leaf-listener TLS is not reloadable" is already finding-001 section 5.2. It stands unchanged; item 4 above is about authorization reloads, which are a different change.

## 3. Not findings, routed elsewhere

These are doc fixes, not graph content:
- the second cluster's recipe file still names the hub's old LAN address as the hub address.
- `sim/design/bus-credential-enrollment.md:65` and `sim/notes/builder-handoff-parts1-2a-2026-09-18.md:81` show `credential put bus/leaf ... --stdin`; marvel accepts `--value-file <file|->`.

## 4. Edges

- extends: finding-001 (mechanics on 2.14.6)
- corrects: finding-002 (the rehearsed SAN set; its method still applies)
- informs: aae-orc#461 stages 5 to 8

## 5. Evidence

The research seat's note and stage 7 runbook (`2026-10-03-hub-leaf-tls-7442.md`, `2026-10-03-stage7-hub-runbook-corporate.md`, in the orchestrator's `.session/research/`, not committed). My re-checks: the interface table for the old address, the established-connection table for the client port, and the hub's own log for the three reloads and the absence of a leaf close beside each.

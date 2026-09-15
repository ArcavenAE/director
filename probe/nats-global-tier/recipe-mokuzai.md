# Joining the global tier from mokuzai (skippy's cluster)

The hub runs on kinu, LAN address 192.168.100.110, leaf port 7442. Your
supervisor keeps talking to your own local broker; the leaf link carries the
director channel. Nothing in your supervisor changes until the shim's global
mode ships; then it is three environment variables in the cast.

## 0. What you receive out of band

One file from the operator, handed privately (never on GitHub, never on the
bus): the NKey seed for your cluster's leaf link. Store it as
`~/.director/nats/leaf-mokuzai.nk`, mode 0600. It is a bus credential the
operator can revoke and re-mint; it is not your OAuth and it is not a
credential at any third party.

## 1. Broker config (local nats-server.conf)

Your broker needs a JetStream domain and one leaf remote. Adding the domain
to a running broker keeps its streams, buckets, and existing clients working.

```
jetstream {
  store_dir: "<your existing store dir>"
  domain: mokuzai
}
leafnodes {
  remotes: [
    { urls: ["nats-leaf://192.168.100.110:7442"], nkey: $DIRECTOR_LEAF_NKEY }
  ]
}
```

Start the broker with the seed in the environment, never in the file:

```sh
DIRECTOR_LEAF_NKEY="$(grep -m1 '^SU' ~/.director/nats/leaf-mokuzai.nk)" \
  nats-server -c <your nats-server.conf>
```

Your shims reconnect on their own after the restart; presence keys rewrite
within 30s.

If your broker runs the director#4 authorization block, add to the
supervisor user: publish allow `global.director.inbox`, `$JS.global.API.>`,
`$JS.ACK.>`; subscribe allow `global.mokuzai.>`, `_INBOX.>`.

## 2. Verify the link

Broker log shows `Leafnode connection created` and
`JetStream using domains: local "mokuzai", remote "global"`. Then, from any
client of your local broker:

```sh
nats --js-domain global kv put GLOBAL_PRESENCE presence.mokuzai.supervisor.test '{"role":"supervisor"}'
nats --js-domain global stream info GLOBAL_TO_mokuzai      # works
nats --js-domain global stream info GLOBAL_TO_DIRECTOR     # "no responders": the binding holding
```

## 3. The test message across kinu

Your side to the director:

```sh
nats --js-domain global pub global.director.inbox 'hello from mokuzai <nonce>' --jetstream
# expect: Stored in Stream: GLOBAL_TO_DIRECTOR ... Domain: "global"
```

The director answers on `global.mokuzai.supervisor.inbox`; read it:

```sh
nats --js-domain global consumer add GLOBAL_TO_mokuzai sup --pull --deliver new --ack explicit \
  --filter global.mokuzai.supervisor.inbox --defaults
nats --js-domain global consumer next GLOBAL_TO_mokuzai sup --raw
```

A publish to any other cluster's prefix, or a consumer on the director's
stream, fails with "no responders" and stores nothing; that is expected.

## 4. Casting a supervisor onto the global tier

The shim's global mode is built (aae-orc-gvf6k). Cast your supervisor with
three more environment variables and nothing else changes:

```sh
DIRECTOR_GLOBAL_DOMAIN=global DIRECTOR_CLUSTER=mokuzai \
  DIRECTOR_GLOBAL_ROLE=supervisor director-mcp
```

The shim keeps its one connection to your local broker, consumes
`GLOBAL_TO_mokuzai` through the domain, beats presence into `GLOBAL_PRESENCE`
on the same 30s timer as its local presence, and accepts `global://director`
as a send address. `wait_for_message` polls both inboxes and names the tier it
found; `list_roster` shows both with a tier column. All three variables are
validated at spawn: the role is `supervisor` or `director` and nothing else,
and the cluster and domain are subject tokens.

Leave `DIRECTOR_GLOBAL_DOMAIN` unset and the shim behaves exactly as it did
before: local tier only, no hub traffic, no global addresses.

Run `director-mcp --preflight` with those variables set before casting: it
verifies the stream and the bucket through the domain and exits nonzero if the
link or the provisioning is missing, which is what keeps a supervisor from
coming up healthy and unreachable.

To prove the whole path on your host, `probe/nats-global-tier/verify-global-shim.sh`
runs 14 checks against the real hub with a throwaway leaf broker of its own,
leaving your live broker alone.

## What the interim LAN posture does not protect

No TLS on 4242 or 7442: anyone on the LAN can read envelopes on the wire and
can attempt a connection. Without a valid NKey they cannot publish or
subscribe (anonymous connections get an authorization violation), and the
seed never crosses the wire (the client signs a server nonce). Keep secrets
out of message bodies; authority never rides content anyway. TLS and DNS
arrive with the cloud placement.

#!/usr/bin/env bash
# P7: flag-day cutover from the legacy AGENT_INBOX to a per-cluster INBOX on the
# agent.<cluster>. root, and its rollback, on a standalone scratch server.
# Needs P7_URL (a scratch server) and FABTOOL. Cluster token: kc.
set -euo pipefail
U=${P7_URL:?scratch server url}; F=${FABTOOL:?fabtool}; MAP=${MAP:-$(mktemp)}
n() { nats --server "$U" "$@"; }
say() { echo; echo "== $*"; }
counts() { n stream subjects "$1" --json 2>/dev/null | jq -c 'to_entries|map({(.key):.value})|add // {}'; }
ack_n() { n consumer next AGENT_INBOX "$1" --count "$2" --ack >/dev/null 2>&1; }

say "legacy AGENT_INBOX, configured as the live one is"
n stream add AGENT_INBOX --subjects 'agent.*.*.*.inbox' --subjects 'agent.*.*.role.*.inbox' \
  --storage file --retention limits --max-age 24h --discard old --defaults >/dev/null
"$F" pub -s "$U" -subj agent.w.t.a.inbox -n 8 -prefix a >/dev/null
"$F" pub -s "$U" -subj agent.w.t.b.inbox -n 4 -prefix b >/dev/null
"$F" pub -s "$U" -subj agent.w.t.c.inbox -n 3 -prefix c >/dev/null
"$F" pub -s "$U" -subj agent.w.t.role.sup.inbox -n 2 -prefix r >/dev/null
for d in "a-dead agent.w.t.a.inbox" "a-live agent.w.t.a.inbox" "b-live agent.w.t.b.inbox" "r-live agent.w.t.role.sup.inbox"; do
  set -- $d; n consumer add AGENT_INBOX "$1" --filter "$2" --pull --ack explicit --deliver all --defaults >/dev/null
done
ack_n a-dead 2; ack_n a-live 5; ack_n r-live 2
echo "legacy per subject: $(counts AGENT_INBOX)"
echo "unread (above the highest ack floor per address): $("$F" unread -s "$U")"

say "the new INBOX cannot coexist with the legacy subjects"
try() { local out; out=$(n stream add INBOX "$@" --storage file --retention work --defaults 2>&1) && { echo created; n stream rm INBOX -f >/dev/null; } || echo "$out" | grep -o 'nats: error.*'; }
echo -n "agent.kc.>            : "; try --subjects 'agent.kc.>'
echo -n "explicit two patterns : "; try --subjects 'agent.kc.*.*.*.inbox' --subjects 'agent.kc.*.*.role.*.inbox'
echo -n "seat pattern only     : "; try --subjects 'agent.kc.*.*.*.inbox'
echo -n "role pattern only     : "; try --subjects 'agent.kc.*.*.role.*.inbox'

say "cutover: (1) publishers stopped (2) park legacy subjects (3) create INBOX (4) migrate unread"
n stream edit AGENT_INBOX --subjects 'legacy.parked.agent_inbox' -f >/dev/null && echo "legacy parked: $(n stream info AGENT_INBOX --json | jq -c .config.subjects)"
n stream add INBOX --subjects 'agent.kc.*.*.*.inbox' --subjects 'agent.kc.*.*.role.*.inbox' --storage file --retention work --max-age 672h --discard new --dupe-window 20m --defaults >/dev/null && echo "INBOX created"
"$F" migrate -s "$U" -cluster kc -map "$MAP"
echo "INBOX per subject: $(counts INBOX)"
echo "legacy still holds its messages for rollback: $(n stream info AGENT_INBOX --json | jq .state.messages)"
echo -n "rerun migrate (idempotent by Nats-Msg-Id): "; "$F" migrate -s "$U" -cluster kc -map "$MAP.rerun" >/dev/null; echo "$(counts INBOX)"

say "new era: one durable per address, one migrated message read, three new messages"
n consumer add INBOX a --filter agent.kc.w.t.a.inbox --pull --ack explicit --deliver all --defaults >/dev/null
n consumer next INBOX a --ack >/dev/null 2>&1
"$F" pub -s "$U" -subj agent.kc.w.t.a.inbox -n 2 -prefix new-a >/dev/null
"$F" pub -s "$U" -subj agent.kc.w.t.b.inbox -n 1 -prefix new-b >/dev/null
echo "INBOX per subject: $(counts INBOX)"

say "rollback: (1) publishers stopped (2) park INBOX (3) restore legacy subjects (4) reconcile"
n stream edit INBOX --subjects 'legacy.parked.inbox' -f >/dev/null
n stream edit AGENT_INBOX --subjects 'agent.*.*.*.inbox' --subjects 'agent.*.*.role.*.inbox' -f >/dev/null && echo "legacy restored: $(n stream info AGENT_INBOX --json | jq -c .config.subjects)"
"$F" rollback -s "$U" -cluster kc -map "$MAP"
echo "unread after rollback: $("$F" unread -s "$U")"
echo "legacy durables' pending: $(for c in a-live b-live r-live; do n consumer info AGENT_INBOX $c --json | jq -r '"\(.name)=\(.num_pending)"'; done | tr '\n' ' ')"
echo "expected: a 4 (3 unread - 1 read after cutover + 2 new), b 5 (4 + 1 new), c 3, role 0"

# finding-022: a hub config reload that removes a user closes that user's live client connection at once, and its reconnects are refused

- **Date:** 2026-10-04
- **Session:** the arcaven builder seat, placing the 2026-10-04 team harvest. The measurement is the pull-request reviewer's, in marvel#519 review 5400892009, section (c); I have not rerun it.
- **Subject:** how a permission change on the hub reaches a connection that is already open (R-86). It extends finding-001 section 5 and finding-009.
- **Confidence:** measured once, on a scratch nats-server, on a plain client connection. A leaf connection was not measured.

## 0. The sentence

**Removing a user from the server config and reloading closes that user's live client connection immediately, and the user's reconnect attempts and fresh publishes are then refused with an authorization error, so per-user revocation takes effect at reload without restarting the seat. This was measured on a client connection only; whether a removed user's leaf connection behaves the same is not known.**

## 1. What was observed

The reviewer configured two users, connected one subscriber as each, and listed both in `/connz?auth=true`. They removed one user from the config and sent the reload signal. The server logged `Reloaded: authorization users`. The removed user's subscriber was disconnected at once (`Disconnected due to: EOF`) and `/connz` then showed only the other user. That client's reconnects and a fresh publish as that user were refused (`authentication error`, `Authorization Violation`).

The seat is left cut off and retrying. A restart is what gives it a new user, so the doc under review could state that in place of its conditional.

## 2. How it sits with the earlier findings

- finding-009 recorded the opposite direction on a leaf: a grant added by reload did not reach a leaf connection opened before the reload. Adding and removing are not symmetric on what was measured: removal closed the client connection, while an added grant did not reach an established leaf.
- finding-001 section 5 measured TLS cutover mechanics on reload (the client listener takes TLS by reload, the leaf listener does not). This is about authorization on reload, not TLS, and does not change section 5.
- finding-019 item 4: a FLEET user reload keeps live leafs connected. Removing a user is a different change from adding or changing one, and finding-019 did not test it.

## 3. What this does not establish

- A leaf connection for a removed user. That is the case a hub operator would hit first, and it is unmeasured.
- Any version beyond the scratch server the reviewer ran.
- Whether the closed user's seat shim retries or exits (marvel-builder's open question MQ1 in the team harvest).

## 4. Edges

- extends: finding-001 (section 5), finding-009
- relates: finding-019
- evidence: marvel#519 review 5400892009 (section c)

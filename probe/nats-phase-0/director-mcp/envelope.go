package main

// Routing identity and shim-local constants. The wire envelope itself is the
// canonical contract type in contracts/envelope (generated from marvel's
// director-envelope schema, vendored and sha-pinned); this file keeps only what
// the shim needs for in-process routing and presence, which the wire type does
// not carry.

// Sender is the shim's routing view of itself. Team is in-process routing only
// (subjects and presence keys); it is not a wire field, because team lives on
// the recipient address, not the sender. AgentID and Workspace are validated as
// identity tokens at spawn (validateIdentity, director#3).
type Sender struct {
	AgentID   string
	Role      string
	Workspace string
	Team      string
}

// maxEnvelopeBytes caps a marshaled envelope; anything larger must go by
// pointer in content.refs (envelope design section 2.4).
const maxEnvelopeBytes = 65536

// director-mcp: a Phase 0 probe shim. An MCP server over stdio that carries
// director envelopes over a NATS bus, so any MCP-capable harness can send to,
// and poll for, messages from other sessions. This is Mr. RightNow: it proves
// the transport and measures the receive shape. It is not director.
//
// Launch (per agent), by a harness as an MCP stdio server:
//
//	DIRECTOR_AGENT_ID=reviewer-a DIRECTOR_TEAM=ops DIRECTOR_WORKSPACE=aae-orc \
//	NATS_URL=nats://127.0.0.1:4222 director-mcp
//
// The three identity values are subject tokens and must match [A-Za-z0-9_-];
// anything else is refused at startup (director#3). Optional broker
// credentials: DIRECTOR_NATS_CREDS (a .creds file) or DIRECTOR_NATS_USER with
// DIRECTOR_NATS_PASS; unset means an anonymous connection, as before.
//
// Logs go to stderr so stdout stays clean JSON-RPC.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	self := Sender{
		AgentID:   env("DIRECTOR_AGENT_ID", ""),
		Team:      env("DIRECTOR_TEAM", "default"),
		Workspace: env("DIRECTOR_WORKSPACE", "default"),
	}
	if self.AgentID == "" {
		fmt.Fprintln(os.Stderr, "director-mcp: DIRECTOR_AGENT_ID is required")
		os.Exit(2)
	}
	// Reject a malformed identity at spawn (director#3, R-76 and R-78): the
	// launcher-assigned levers are checked against the closed subject-token
	// class and the shim exits before any subject is built. Rejected, never
	// rewritten.
	if err := validateIdentity(self); err != nil {
		fmt.Fprintf(os.Stderr, "director-mcp: %v\n", err)
		os.Exit(2)
	}
	url := env("NATS_URL", "nats://127.0.0.1:4222")

	logf := func(format string, a ...any) {
		log.Printf("[director-mcp %s] "+format, append([]any{self.AgentID}, a...)...)
	}
	log.SetOutput(os.Stderr)

	ctx := context.Background()
	bus, err := connect(ctx, url, self)
	if err != nil {
		fmt.Fprintf(os.Stderr, "director-mcp: bus connect failed: %v\n", err)
		os.Exit(1)
	}
	defer bus.close()
	// Announce presence on start so a roster lists us immediately.
	_ = bus.setPresence(ctx, "idle")
	if warn := bus.checkCollision(ctx); warn != "" {
		logf("WARNING: %s", warn)
	}
	// Renew presence on the shim's own timer, not the model's poll (R-56). The
	// presence bucket TTL is 90s; renew at roughly TTL/3.
	go bus.heartbeat(ctx, 30*time.Second)
	logf("connected to %s as agent://%s/%s instance %s in workspace %s", url, self.Team, self.AgentID, bus.instance, self.Workspace)

	srv := newServer(bus, logf)
	if err := srv.serve(ctx, os.Stdin); err != nil {
		logf("serve ended: %v", err)
	}
}

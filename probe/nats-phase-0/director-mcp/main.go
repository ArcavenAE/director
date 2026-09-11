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
// Logs go to stderr so stdout stays clean JSON-RPC.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
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
	logf("connected to %s as agent://%s/%s in workspace %s", url, self.Team, self.AgentID, self.Workspace)

	srv := newServer(bus, logf)
	if err := srv.serve(ctx, os.Stdin); err != nil {
		logf("serve ended: %v", err)
	}
}

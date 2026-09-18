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
// The global tier (R-86) is off unless DIRECTOR_GLOBAL_DOMAIN names the hub's
// JetStream domain, and needs two more levers when it is on:
//
//	DIRECTOR_GLOBAL_DOMAIN=global DIRECTOR_CLUSTER=mokuzai \
//	DIRECTOR_GLOBAL_ROLE=supervisor director-mcp
//
// The shim then keeps its one connection to the local broker and reaches the
// hub through that broker's leaf link: it consumes its cluster's global inbox,
// beats presence into GLOBAL_PRESENCE, and accepts global:// addresses. It
// holds no hub credential; the leaf link does (sim/design/global-bus-tier.md).
//
// Logs go to stderr so stdout stays clean JSON-RPC.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	// --preflight: connect, verify the broker is provisioned, and exit. Used by
	// cast-launch before starting the harness (finding-166, R-93).
	preflightMode := false
	for _, a := range os.Args[1:] {
		if a == "--preflight" || a == "-preflight" {
			preflightMode = true
		}
	}
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
	// The global levers are validated here, before anything connects, for the
	// same reason the local identity is: a bad role or a cluster name that is
	// not a subject token would build a subject that is not ours, so it is
	// refused at spawn and never rewritten (R-76, R-94).
	gcfg, err := loadGlobalConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "director-mcp: %v\n", err)
		os.Exit(2)
	}

	if preflightMode {
		if err := preflight(context.Background(), url, self, gcfg); err != nil {
			fmt.Fprintf(os.Stderr, "director-mcp preflight: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "director-mcp preflight: ok")
		return
	}

	logf := func(format string, a ...any) {
		log.Printf("[director-mcp %s] "+format, append([]any{self.AgentID}, a...)...)
	}
	log.SetOutput(os.Stderr)

	// A signal-aware context so a SIGTERM/SIGINT (the harness stopping the MCP
	// server, or an operator) cancels the heartbeat and the serve loop and
	// reaches the writer-side deregister below, instead of the process dying
	// with defers unrun and a live-looking presence row left behind (BEAT-C).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	bus, err := connect(ctx, url, self, gcfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "director-mcp: bus connect failed: %v\n", err)
		os.Exit(1)
	}
	defer bus.close()
	// Announce presence on start so a roster lists us immediately.
	globalWarn, _ := bus.setPresence(ctx, "idle")
	bus.reportGlobalWarn(globalWarn, logf)
	if warn := bus.checkCollision(ctx); warn != "" {
		logf("WARNING: %s", warn)
	}
	// Renew presence on the shim's own timer, not the model's poll (R-56). The
	// presence bucket TTL is 90s; renew at roughly TTL/3. The same tick carries
	// the global row and re-attaches a hub that was down at startup.
	go bus.heartbeat(ctx, 30*time.Second, logf)
	logf("connected to %s as agent://%s/%s instance %s in workspace %s", url, self.Team, self.AgentID, bus.instance, self.Workspace)
	if gcfg != nil {
		logf("global tier on: %s, consuming %s from stream %s in domain %q", gcfg.selfAddress(), gcfg.inboxSubject(), gcfg.streamName(), gcfg.Domain)
	}

	srv := newServer(bus, logf)
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.serve(ctx, os.Stdin) }()
	select {
	case err := <-serveErr:
		if err != nil {
			logf("serve ended: %v", err)
		}
	case <-ctx.Done():
		logf("shutdown signal received, deregistering presence")
	}
	// BEAT-C: delete this session's presence on an orderly exit. ctx is already
	// cancelled on the signal path, so deregister runs on a fresh bounded
	// context; bus.close() (the deferred drain) then closes the connection.
	dctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	bus.deregister(dctx, logf)
	cancel()
}

package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// role://director is the one-segment address of the director (seat-id Q2). A
// send to it takes the global route, as global://director does, so a seat with
// the global tier off is refused for that reason and not for a malformed role
// address.
func TestSendToRoleDirectorTakesTheGlobalRoute(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	b := roleBus(t, ctx, url, "seat-1", "")
	for _, addr := range []string{"global://director", "role://director"} {
		_, err := sendRole2(ctx, b, addr)
		if err == nil || !strings.Contains(err.Error(), "the global tier is off") {
			t.Errorf("%s: want the global-tier-off refusal, got %v", addr, err)
		}
	}
	// A team role keeps the local form and its own refusal.
	if _, err := sendRole2(ctx, b, "role://director/"); err == nil || strings.Contains(err.Error(), "global tier") {
		t.Errorf("role://director/ must not take the global route, got %v", err)
	}
}

func sendRole2(ctx context.Context, from *Bus, addr string) (*sendResult, error) {
	e := testEnv("m-"+strings.ReplaceAll(addr, "/", "-"), from.self.AgentID, "INFORM", "to the director")
	e.Recipient.Address = addr
	return from.publish(ctx, e, "")
}

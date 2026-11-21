package main

import "testing"

// The stalled reason names the oldest message the seat will be handed. oldestMetaOn
// read the global tier with a peek of one and no filter, so a message the FOR
// filter will drop, still in the durable ahead of a kept one, was named instead
// (director#731).

// The first message is for a live seat of another team and waits unread; the
// second has no FOR line. The reason must name the second.
func TestOldestMetaOnGlobalSkipsMailTheFilterDrops(t *testing.T) {
	r, _ := forPathRig(t)
	sub := r.bus.global.cfg.inboxSubject()
	pubEnv(t, r.ctx, r.gjs, sub, "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	pubEnv(t, r.ctx, r.gjs, sub, "plain1", "INFORM", "no FOR line")
	m, err := busCueSource{r.bus}.oldestMetaOn(r.ctx, "global")
	if err != nil {
		t.Fatalf("oldestMetaOn: %v", err)
	}
	if m == nil || m.MessageID != "plain1" {
		t.Fatalf("oldestMetaOn named %+v, want plain1 (drop1 is dropped by the filter)", m)
	}
}

// Guard: with only the kept message, it is the one named.
func TestOldestMetaOnGlobalNamesTheKeptMessage(t *testing.T) {
	r, _ := forPathRig(t)
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "plain1", "INFORM", "no FOR line")
	m, err := busCueSource{r.bus}.oldestMetaOn(r.ctx, "global")
	if err != nil {
		t.Fatalf("oldestMetaOn: %v", err)
	}
	if m == nil || m.MessageID != "plain1" {
		t.Fatalf("oldestMetaOn named %+v, want plain1", m)
	}
}

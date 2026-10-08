package main

import (
	"errors"
)

// Stubs for the fabric address derivations in sim/design/global-per-seat-inbox.md
// section 3. The red commit: every function refuses, so the tests that pin the
// design fail. The next commit implements them.

type fabricAddr struct {
	Kind      string // "agent", "role" or "director"
	Cluster   string
	Workspace string
	Team      string
	ID        string // the seat id for an agent address, the role word for a role address
}

type fabricSeat struct {
	Cluster string
	Addr    fabricAddr
	Role    string
}

var errFabricStub = errors.New("fabric address: not implemented")

func parseFabricAddress(s string) (fabricAddr, error) { return fabricAddr{}, errFabricStub }
func (a fabricAddr) subject() string                  { return "" }
func (a fabricAddr) durable() string                  { return "" }
func resolveSupervisorAlias(addr string, seats []fabricSeat) (fabricAddr, error) {
	return fabricAddr{}, errFabricStub
}
func clusterCut(get func(key string) error, cluster string) (bool, error) {
	return false, errFabricStub
}

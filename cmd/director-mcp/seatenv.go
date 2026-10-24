package main

import (
	"errors"
	"os"
)

// seatEnv is what the seat launch resolves from its environment before it
// validates anything or connects: the identity it registers under, and the
// broker user and password when it must supply them itself. An empty natsUser
// leaves the credential the environment already carries to dial.
type seatEnv struct {
	self     Sender
	natsUser string
	natsPass string
}

// resolveSeatEnv reads the seat launch's identity from the environment.
func resolveSeatEnv() (seatEnv, error) {
	se := seatEnv{self: Sender{
		AgentID:   os.Getenv("DIRECTOR_AGENT_ID"),
		Role:      os.Getenv("DIRECTOR_ROLE"),
		Team:      env("DIRECTOR_TEAM", "default"),
		Workspace: env("DIRECTOR_WORKSPACE", "default"),
	}}
	if se.self.AgentID == "" {
		return se, errors.New("DIRECTOR_AGENT_ID is required")
	}
	return se, nil
}

package main

// Stub for the red run: the launcher-environment reader is not written yet.

import "errors"

// hubProcEnv returns the environment of the hub's server process. It is a
// variable so tests can run where the setuid ps cannot.
var hubProcEnv = realHubProcEnv

func realHubProcEnv(pid int) (map[string]string, error) {
	return nil, errors.New("not implemented")
}

// parseProcEnv reads the NAME=VALUE words that `ps eww` prints after the command line.
func parseProcEnv(s string) map[string]string { return nil }

// hubSettingLiteral is how init writes a value into settings.conf.
func hubSettingLiteral(v string) string { return "\"" + v + "\"" }

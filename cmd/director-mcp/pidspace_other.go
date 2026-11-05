//go:build !darwin && !linux

package main

// platformPidspace is empty where no pid-space identity is read, so a shim there
// reaps nothing and its rows are never reaped.
func platformPidspace() string { return "" }

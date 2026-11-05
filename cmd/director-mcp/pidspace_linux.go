package main

// platformPidspace is this process's pid namespace on linux.
func platformPidspace() string { return linuxPidNamespace("/proc/self/ns/pid") }

package main

// platformPidspace is this host's boot id and this process's pid namespace on
// linux (reap.go).
func platformPidspace() string {
	return linuxPlatformPidspace("/proc/sys/kernel/random/boot_id", "/proc/self/ns/pid")
}

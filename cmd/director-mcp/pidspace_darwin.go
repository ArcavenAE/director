package main

import "syscall"

// platformPidspace is the boot session on darwin, which has no pid namespaces
// and so needs only the host name beside it; the boot session also tells a row
// written before a reboot from one written after.
func platformPidspace() string {
	s, err := syscall.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return ""
	}
	return s
}

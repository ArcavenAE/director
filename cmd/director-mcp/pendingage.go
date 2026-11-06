package main

// The pending-age bound (I-190:1). A seat's presence row carries the age of its
// oldest waiting message; this is the bound a reader holds that age against.
// list_roster flags a live row over it and the unread reader marks a live
// recipient over it. The bound is diagnostic only: it changes no exit status,
// no send and no wake (R-113, SOUL section 8).

import (
	"fmt"
	"io"
	"time"
)

// envPendingAgeBound is the one override. It takes a Go duration and is read
// where the reader runs, at startup.
const envPendingAgeBound = "DIRECTOR_PENDING_AGE_BOUND"

// pendingAgeBound is the bound in force. main sets it from the environment at
// startup; a test sets it directly.
var pendingAgeBound = defaultPendingAgeBound()

// loadPendingAgeBound reads the override. A value that does not parse as a Go
// duration, or is 0 or less, is a warning on warn and the default applies.
func loadPendingAgeBound(getenv func(string) string, warn io.Writer) time.Duration {
	v := getenv(envPendingAgeBound)
	if v == "" {
		return defaultPendingAgeBound()
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		_, _ = fmt.Fprintf(warn, "director-mcp: %s=%q is not a positive Go duration such as 15m; using the default %s\n", envPendingAgeBound, v, defaultPendingAgeBound())
		return defaultPendingAgeBound()
	}
	return d
}

// ageSeconds reads a presence row's oldest_pending_s, which is a float64 once
// decoded from JSON and an int64 when a record is built in process.
func ageSeconds(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

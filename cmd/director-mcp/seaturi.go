package main

// The seat URI, marvel://<fleet>/<cluster>/<ws>/<team>/<slot> with
// <slot> = <role>-<idx>: the one constant and builder in this codebase, so
// every projection of a seat's identity is cut from the same string and
// agrees with marvel's byte for byte (CAND-ID-6, aae-orc
// docs/design/seat-identity-grammar.md section 3.1 and 4).
//
// Segments are validated as raw strings and never rewritten (CAND-ID-1,
// CAND-ID-2): url.Parse decodes escapes in Path, so a validator that read the
// parsed form would pass a string a byte compare would not match.

import (
	"fmt"
	"strconv"
	"strings"
)

const seatURIScheme = "marvel://"

// seatURISegments is the count after the scheme (CAND-ID-4).
const seatURISegments = 5

// SeatURI is the parsed form of a seat URI. The slot is Role-Index.
type SeatURI struct {
	Fleet, Cluster, Workspace, Team string
	Role                            string
	Index                           int
}

// validSegment reports CAND-ID-1: lowercase letters and digits in runs joined
// by single hyphens, with no leading, trailing or doubled hyphen.
func validSegment(s string) bool {
	if s == "" || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	prev := byte('-')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && prev != '-':
		default:
			return false
		}
		prev = c
	}
	return true
}

// oldGenerationTail reports whether role ends in -g<digits>, the shape of an
// old generation-bearing session id (CAND-ID-3).
func oldGenerationTail(role string) bool {
	i := strings.LastIndex(role, "-g")
	if i < 0 || i+2 == len(role) {
		return false
	}
	for _, c := range role[i+2:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// splitSlot parses <role>-<idx> at its last hyphen (CAND-ID-3).
func splitSlot(slot string) (role string, idx int, err error) {
	if !validSegment(slot) {
		return "", 0, fmt.Errorf("slot %q is not a valid segment", slot)
	}
	i := strings.LastIndex(slot, "-")
	if i < 0 {
		return "", 0, fmt.Errorf("slot %q has no -<index> suffix", slot)
	}
	role, tail := slot[:i], slot[i+1:]
	if tail == "" || (len(tail) > 1 && tail[0] == '0') {
		return "", 0, fmt.Errorf("slot %q: index %q has a leading zero or is empty", slot, tail)
	}
	for _, c := range tail {
		if c < '0' || c > '9' {
			return "", 0, fmt.Errorf("slot %q: index %q is not decimal digits", slot, tail)
		}
	}
	idx, err = strconv.Atoi(tail)
	if err != nil {
		return "", 0, fmt.Errorf("slot %q: index %q: %v", slot, tail, err)
	}
	if !validSegment(role) {
		return "", 0, fmt.Errorf("slot %q: role %q is not a valid segment", slot, role)
	}
	if oldGenerationTail(role) {
		return "", 0, fmt.Errorf("slot %q: role %q ends in -g<digits>, the old generation-bearing shape", slot, role)
	}
	return role, idx, nil
}

// Slot is <role>-<index>.
func (u SeatURI) Slot() string { return u.Role + "-" + strconv.Itoa(u.Index) }

// String is the URI. It does not validate; use NewSeatURI to build one.
func (u SeatURI) String() string {
	return seatURIScheme + strings.Join([]string{u.Fleet, u.Cluster, u.Workspace, u.Team, u.Slot()}, "/")
}

// NewSeatURI is the builder: it validates every part and returns the seat.
func NewSeatURI(fleet, cluster, ws, team, role string, idx int) (SeatURI, error) {
	if idx < 0 {
		return SeatURI{}, fmt.Errorf("index %d is negative", idx)
	}
	u := SeatURI{Fleet: fleet, Cluster: cluster, Workspace: ws, Team: team, Role: role, Index: idx}
	for _, p := range []struct{ name, v string }{{"fleet", fleet}, {"cluster", cluster}, {"workspace", ws}, {"team", team}} {
		if !validSegment(p.v) {
			return SeatURI{}, fmt.Errorf("%s %q is not a valid segment", p.name, p.v)
		}
	}
	if _, _, err := splitSlot(u.Slot()); err != nil {
		return SeatURI{}, err
	}
	return u, nil
}

// ParseSeatURI parses the raw string. A string that does not match is
// rejected, never rewritten.
func ParseSeatURI(raw string) (SeatURI, error) {
	if !strings.HasPrefix(raw, seatURIScheme) {
		return SeatURI{}, fmt.Errorf("seat URI %q does not start with %s", raw, seatURIScheme)
	}
	rest := raw[len(seatURIScheme):]
	if strings.HasSuffix(rest, "/") {
		return SeatURI{}, fmt.Errorf("seat URI %q has a trailing slash", raw)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != seatURISegments {
		return SeatURI{}, fmt.Errorf("seat URI %q has %d segments, want %d", raw, len(parts), seatURISegments)
	}
	role, idx, err := splitSlot(parts[4])
	if err != nil {
		return SeatURI{}, err
	}
	return NewSeatURI(parts[0], parts[1], parts[2], parts[3], role, idx)
}

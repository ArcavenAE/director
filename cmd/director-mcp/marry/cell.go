// Package marry is director's port of the idle scheduler's marrying step and
// the seat readiness routing rule, which marvel carried in internal/api
// (aae-orc I-241, card 1). Director owns work management, so the pairing of
// ready items with free seats lives here and needs no marvel binary (ADR-005).
// The two contracts it must keep are marvel's golden vectors, pinned in
// testdata/ and run by the tests.
//
// Everything here is a pure function over its arguments: it reads no bd, no
// clock but the one it is given, and sends nothing.
package marry

import "time"

// CellState is the freshness word of a Cell.
type CellState string

const (
	// CellFresh: the reading is observed and has not expired.
	CellFresh CellState = "fresh"
	// CellStale: the reading was observed and has expired.
	CellStale CellState = "stale"
	// CellNone: the reading was never observed.
	CellNone CellState = "none"
)

// Cell is a reading with its age, in marvel's asof.Cell JSON shape (the vectors
// carry it). A zero ObservedAt means never observed, whatever Value holds. A
// zero ValidUntil means the reading does not expire. Source names where the
// reading came from.
type Cell struct {
	Value      string    `json:"value,omitzero"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
	ValidUntil time.Time `json:"valid_until,omitzero"`
	Source     string    `json:"source,omitempty"`
}

// State reports the cell's freshness at now. A reading is still fresh at
// exactly ValidUntil.
func (c Cell) State(now time.Time) CellState {
	switch {
	case c.ObservedAt.IsZero():
		return CellNone
	case !c.ValidUntil.IsZero() && now.After(c.ValidUntil):
		return CellStale
	default:
		return CellFresh
	}
}

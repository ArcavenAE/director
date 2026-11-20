package main

// A mirror seat that stands in for a swapped-out team (DIRECTOR_STANDS_IN_FOR,
// runbook ticket A). The value is one team name. With it set, the FOR filter
// treats the same role's names in that team as this seat's own, so mail the
// hub fans out for the replaced seat is delivered to its mirror instead of
// dropped as meant for another seat (forfilter.go: namesSelf, otherSeat). It is
// read once at launch (seatenv.go) and published on both presence rows as
// stands_in_for. Unset, nothing changes. A value that cannot be used is logged
// and ignored, never refused: every doubt delivers.

import "fmt"

// parseStandsInFor returns the team a seat stands in for. An empty raw value
// is the unset case: no team and no reason. A value that is not one subject
// token, or that names the seat's own team, gives no team and the reason to
// log, which names the value.
func parseStandsInFor(raw, ownTeam string) (team, why string) {
	if raw == "" {
		return "", ""
	}
	if err := validToken("DIRECTOR_STANDS_IN_FOR", raw); err != nil {
		return "", fmt.Sprintf("director-mcp: DIRECTOR_STANDS_IN_FOR %q ignored: %v", raw, err)
	}
	if raw == ownTeam {
		return "", fmt.Sprintf("director-mcp: DIRECTOR_STANDS_IN_FOR %q ignored: a seat cannot stand in for its own team", raw)
	}
	return raw, ""
}

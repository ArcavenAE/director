package marry

import (
	"strings"
	"time"
)

// Readiness routing (marvel docs/design/seat-readiness.md section 6): which
// seats a job may be placed on, read from the duty.<name> and withdrawn.<duty>
// cells and nothing else. The rule is a pure function so that director and
// marvel route identically; routing-vectors.json is the shared test.
//
// RouteSeat reads cells and a clock. It reads no count, rate or time threshold,
// so none of them can exclude a seat (ADR-007): the clock only decides whether
// a cell is fresh, and an expired cell asks first.

// RouteVerdict is the router's answer for a seat, or for one duty of it.
type RouteVerdict string

const (
	// RouteExcluded: off the free list, with the reason shown (rule 1).
	RouteExcluded RouteVerdict = "excluded"
	// RouteEligible: a fresh proven value for every duty the job needs (rule 2).
	RouteEligible RouteVerdict = "eligible"
	// RouteAskFirst: neither excluded nor proven; never assumed (rule 3).
	RouteAskFirst RouteVerdict = "ask-first"
)

// RouteReason is the code for a duty's verdict: a fixed word, never pane text
// or an error string.
type RouteReason string

const (
	ReasonWithdrawn      RouteReason = "withdrawn"
	ReasonStructuralFail RouteReason = "structural-fail"
	ReasonProven         RouteReason = "proven"
	ReasonHostVantage    RouteReason = "host-vantage"
	ReasonTransientFail  RouteReason = "transient-fail"
	ReasonUnproven       RouteReason = "unproven"
	ReasonNeverObserved  RouteReason = "never-observed"
	ReasonExpired        RouteReason = "expired"
	ReasonOverridden     RouteReason = "overridden"
	ReasonNoDuties       RouteReason = "no-duties"
)

// Duty cell values.
const (
	dutyProven    = "proven"
	dutyFail      = "fail"
	dutyWithdrawn = "withdrawn"
)

// DutyCellName and WithdrawnCellName are the cell names of a duty.
func DutyCellName(duty string) string      { return "duty." + duty }
func WithdrawnCellName(duty string) string { return "withdrawn." + duty }

// RouteOverride is a supervisor naming a seat for a job (rule 1). It applies
// only to the seat it names, and only when By names who.
type RouteOverride struct {
	Seat string `json:"seat"`
	By   string `json:"by"`
}

// RouteInput is everything the rule reads for one seat and one job.
type RouteInput struct {
	Seat string `json:"seat"`
	// Duties are the duties the job needs, as the role resolver gives them.
	Duties   []string        `json:"duties"`
	Cells    map[string]Cell `json:"cells"`
	Override *RouteOverride  `json:"override,omitempty"`
}

// DutyRoute is the verdict for one duty, with the cell source behind it.
type DutyRoute struct {
	Duty    string       `json:"duty"`
	Verdict RouteVerdict `json:"verdict"`
	Reason  RouteReason  `json:"reason"`
	Source  string       `json:"source,omitempty"`
}

// RouteDecision is the router's answer for a seat.
type RouteDecision struct {
	Verdict RouteVerdict `json:"verdict"`
	// Reason is set when the seat-level verdict has a reason of its own
	// (no-duties); otherwise each duty carries its reason.
	Reason       RouteReason `json:"reason,omitempty"`
	Duties       []DutyRoute `json:"duties,omitempty"`
	OverriddenBy string      `json:"overridden_by,omitempty"`
}

// RouteSeat applies the routing rule to one seat at now. Each duty the job
// needs gets a verdict; the seat is excluded if any duty is, eligible only if
// every duty is, and asks first otherwise. A supervisor override that names
// this seat lifts each exclusion to ask first, never to eligible: naming a seat
// is a choice to ask it, not evidence that the duty works.
func RouteSeat(in RouteInput, now time.Time) RouteDecision {
	if len(in.Duties) == 0 {
		return RouteDecision{Verdict: RouteAskFirst, Reason: ReasonNoDuties}
	}
	override := in.Override != nil && in.Override.By != "" && in.Override.Seat == in.Seat
	out := RouteDecision{Verdict: RouteEligible, Duties: make([]DutyRoute, 0, len(in.Duties))}
	lifted := false
	for _, duty := range in.Duties {
		d := routeDuty(duty, in.Cells, now)
		if d.Verdict == RouteExcluded && override {
			d.Verdict, d.Reason = RouteAskFirst, ReasonOverridden
			lifted = true
		}
		out.Duties = append(out.Duties, d)
		switch {
		case d.Verdict == RouteExcluded:
			out.Verdict = RouteExcluded
		case d.Verdict == RouteAskFirst && out.Verdict == RouteEligible:
			out.Verdict = RouteAskFirst
		}
	}
	if lifted {
		out.OverriddenBy = in.Override.By
	}
	return out
}

// routeDuty is the verdict for one duty. A withdrawal is read first and never
// expires. Then the duty cell: a fresh structural fail excludes, a fresh proven
// value from the seat's own vantage is eligible, and everything else asks first.
func routeDuty(duty string, cells map[string]Cell, now time.Time) DutyRoute {
	if w, ok := cells[WithdrawnCellName(duty)]; ok && w.Value == dutyWithdrawn && !w.ObservedAt.IsZero() {
		return DutyRoute{Duty: duty, Verdict: RouteExcluded, Reason: ReasonWithdrawn, Source: w.Source}
	}
	c := cells[DutyCellName(duty)]
	d := DutyRoute{Duty: duty, Verdict: RouteAskFirst, Source: c.Source}
	switch c.State(now) {
	case CellNone:
		d.Reason = ReasonNeverObserved
	case CellStale:
		d.Reason = ReasonExpired
	default:
		switch c.Value {
		case dutyProven:
			if dutySourceVantage(c.Source) == "host" {
				d.Reason = ReasonHostVantage
			} else {
				d.Verdict, d.Reason = RouteEligible, ReasonProven
			}
		case dutyFail:
			if structuralDutySource(c.Source) {
				d.Verdict, d.Reason = RouteExcluded, ReasonStructuralFail
			} else {
				d.Reason = ReasonTransientFail
			}
		default:
			d.Reason = ReasonUnproven
		}
	}
	return d
}

// dutySourceVantage is the vantage word of a Source.
func dutySourceVantage(src string) string {
	v, _, _ := strings.Cut(src, ":")
	return v
}

// matchedDialogClasses are the dialog pattern classes. A pane:dialog code is a
// matched dialog only when it names one of them; any other code there is not a
// match, so it never excludes.
var matchedDialogClasses = map[string]bool{"consent": true, "trust": true, "folder-trust": true, "logged-out": true}

// structuralDutySource reports whether a fail's Source is one of the codes that
// may exclude: the far side refused the credential, the watcher saw a denial,
// or a dialog pattern matched. Every reach code, rate-limited, and anything
// under dialog that is not a matched pattern class are transient.
func structuralDutySource(src string) bool {
	switch src {
	case "seat:auth:auth-rejected", "seat:auth:forbidden":
		return true
	}
	parts := strings.Split(src, ":")
	if len(parts) < 2 {
		return false
	}
	switch parts[1] {
	case "denial":
		return parts[0] == "seat" || parts[0] == "pane"
	case "dialog":
		return parts[0] == "pane" && len(parts) == 3 && matchedDialogClasses[parts[2]]
	}
	return false
}

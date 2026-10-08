package main

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

// The per-seat fabric address derivations from sim/design/global-per-seat-inbox.md
// section 3 and design brief 11 section 2.1. Everything here is a pure function
// of its arguments: no broker, no environment. Nothing in the live publish or
// receive path calls it yet, so adding it switches nothing over; a later change
// wires it behind the cutover plan.

const (
	fabricKindAgent    = "agent"
	fabricKindRole     = "role"
	fabricKindDirector = "director"
)

// fabricAddr is one parsed fabric address. For an agent address ID is the seat
// id; for a role address ID is the cast role word. The director has no tokens.
type fabricAddr struct {
	Kind      string
	Cluster   string
	Workspace string
	Team      string
	ID        string
}

// fabricSeat is one live seat as the roster reports it: its address and the
// role it was cast with. The alias reads fabric presence only, never legacy
// presence, so a seat whose legacy row still says "supervisor" is not a
// candidate unless its cast role is.
type fabricSeat struct {
	Cluster string
	Addr    fabricAddr
	Role    string
}

// reservedSeatID cannot be a seat id, because the role form of an inbox subject
// is agent.<c>.<ws>.<team>.role.<r>.inbox. audit and broadcast cannot be a
// cluster or a workspace token for the same reason (brief 11 section 2.1).
const reservedSeatID = "role"

var reservedScopeTokens = map[string]bool{"audit": true, "broadcast": true}

// parseFabricAddress accepts exactly three forms and refuses everything else:
//
//	agent://<cluster>/<workspace>/<team>/<id>
//	role://<cluster>/<workspace>/<team>/<role>
//	director
//
// A value outside the identity class is rejected, not rewritten (R-76).
func parseFabricAddress(s string) (fabricAddr, error) {
	if s == "director" {
		return fabricAddr{Kind: fabricKindDirector}, nil
	}
	var kind, rest string
	switch {
	case strings.HasPrefix(s, "agent://"):
		kind, rest = fabricKindAgent, strings.TrimPrefix(s, "agent://")
	case strings.HasPrefix(s, "role://"):
		kind, rest = fabricKindRole, strings.TrimPrefix(s, "role://")
	default:
		return fabricAddr{}, fmt.Errorf("unroutable fabric address %q; the forms are agent://<cluster>/<workspace>/<team>/<id>, role://<cluster>/<workspace>/<team>/<role> and director (a global:// address is an alias, resolved by resolveSupervisorAlias)", s)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 4 {
		return fabricAddr{}, fmt.Errorf("unroutable fabric address %q; a %s address has exactly four tokens, cluster, workspace, team and %s", s, kind, map[string]string{fabricKindAgent: "id", fabricKindRole: "role"}[kind])
	}
	names := [4]string{"fabric cluster", "fabric workspace", "fabric team", "fabric " + kind + " id"}
	for i, p := range parts {
		if err := validToken(names[i], p); err != nil {
			return fabricAddr{}, err
		}
	}
	if reservedScopeTokens[parts[0]] {
		return fabricAddr{}, fmt.Errorf("fabric cluster %q is reserved; audit and broadcast name subjects an inbox must never capture", parts[0])
	}
	if reservedScopeTokens[parts[1]] {
		return fabricAddr{}, fmt.Errorf("fabric workspace %q is reserved; audit and broadcast name subjects an inbox must never capture", parts[1])
	}
	if kind == fabricKindAgent && parts[3] == reservedSeatID {
		return fabricAddr{}, fmt.Errorf("%q cannot be a seat id; agent.<cluster>.<workspace>.<team>.role.<role>.inbox is the role form", reservedSeatID)
	}
	return fabricAddr{Kind: kind, Cluster: parts[0], Workspace: parts[1], Team: parts[2], ID: parts[3]}, nil
}

// String is the address in the form parseFabricAddress accepts.
func (a fabricAddr) String() string {
	if a.Kind == fabricKindDirector {
		return "director"
	}
	return a.Kind + "://" + strings.Join([]string{a.Cluster, a.Workspace, a.Team, a.ID}, "/")
}

// subject is the inbox subject this address is read from.
func (a fabricAddr) subject() string {
	switch a.Kind {
	case fabricKindDirector:
		return "director.inbox"
	case fabricKindRole:
		return "agent." + strings.Join([]string{a.Cluster, a.Workspace, a.Team}, ".") + ".role." + a.ID + ".inbox"
	default:
		return "agent." + strings.Join([]string{a.Cluster, a.Workspace, a.Team, a.ID}, ".") + ".inbox"
	}
}

// durable names the one durable consumer for this address. It carries no
// instance token: a seat's successor binds the same durable, so a second live
// holder of one address is refused at the broker instead of sharing its mail
// (design section 2, rule 2). Tokens may hold "-" and "_", so a plain join
// would let two addresses name one durable; each token is therefore prefixed
// with its length, which keeps the name inside [A-Za-z0-9_-] and unambiguous.
func (a fabricAddr) durable() string {
	if a.Kind == fabricKindDirector {
		return "fab_director"
	}
	parts := []string{a.Cluster, a.Workspace, a.Team, a.ID}
	for i, p := range parts {
		parts[i] = strconv.Itoa(len(p)) + "-" + p
	}
	return "fab_" + a.Kind + "_" + strings.Join(parts, "_")
}

// resolveSupervisorAlias resolves a legacy global:// address against the roster.
// global://director is the director. global://<cluster>/supervisor is the one
// live seat holding the supervisor role on that cluster; with none it refuses
// (R-92), and with more than one it refuses naming the candidates (R-78). It
// never fans out. It reads fabric seats only, so the research seat, cast
// research-supervisor, is never a candidate even while its legacy presence row
// still says supervisor.
func resolveSupervisorAlias(addr string, seats []fabricSeat) (fabricAddr, error) {
	rest, ok := strings.CutPrefix(addr, "global://")
	if !ok || rest == "" {
		return fabricAddr{}, fmt.Errorf("not a global alias: %q; the aliases are global://director and global://<cluster>/supervisor", addr)
	}
	if rest == "director" {
		return fabricAddr{Kind: fabricKindDirector}, nil
	}
	cluster, role, hasSlash := strings.Cut(rest, "/")
	if !hasSlash || role != "supervisor" || strings.Contains(role, "/") {
		return fabricAddr{}, fmt.Errorf("unroutable global alias %q; the aliases are global://director and global://<cluster>/supervisor, nothing else", addr)
	}
	if err := validToken("global cluster", cluster); err != nil {
		return fabricAddr{}, err
	}
	var held []string
	var found fabricAddr
	for _, s := range seats {
		if s.Cluster == cluster && s.Role == "supervisor" {
			held = append(held, s.Addr.String())
			found = s.Addr
		}
	}
	switch len(held) {
	case 0:
		return fabricAddr{}, fmt.Errorf("no live seat holds the supervisor role on cluster %q, so %s is refused rather than stored where nobody reads (R-92 liveness)", cluster, addr)
	case 1:
		return found, nil
	}
	sort.Strings(held)
	return fabricAddr{}, fmt.Errorf("%s is ambiguous: %d seats hold the supervisor role on cluster %q (%s); send to one seat address or its role address, or broadcast for every holder (R-78)", addr, len(held), cluster, strings.Join(held, ", "))
}

// clusterCut reports whether a cluster is listed in the cutover record. get
// asks the record for one key and returns nil when the key exists,
// jetstream.ErrKeyNotFound when it does not, and anything else for a failed
// read. Not found, including from an empty bucket, means the cluster is not cut;
// any other error refuses, because a failed read is never taken as absence
// (design section 5.3).
func clusterCut(get func(key string) error, cluster string) (bool, error) {
	if err := validToken("cutover cluster", cluster); err != nil {
		return false, err
	}
	err := get(cluster)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, jetstream.ErrKeyNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("the cutover record could not be read for cluster %q, so the send is refused rather than guessed: %w", cluster, err)
	}
}

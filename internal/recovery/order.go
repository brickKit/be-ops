package recovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The order of step 6's replay (be-protocol P12.22; assembly protocol 15, the replay table): the evidence records are
// merged by identity (an evidence store's record and the alert channel's copy of it are one), each record replayed
// once in its last phase that counts, grouped by owner, each owner's by (epoch, commit sequence) with its intent-only
// records last; the authorization member last, so it has applied its sources' replay first, and its intent-only
// tightenings apart: they wait for 6.4 and go through its deferred replay (6.5).

// AuthzID is the authorization member, the owner replayed last.
const AuthzID = "infra/authz"

// Evidence is one be.evidence.record as the replay reads it.
type Evidence struct {
	Raw      []byte `json:"-"`
	ID       string `json:"id"`
	Owner    string `json:"owner"`
	Category string `json:"category"`
	Phase    string `json:"phase"`
	Subject  string `json:"subject,omitempty"`
	// Position: the epoch (generation, timeline) and the commit sequence; absent on intent and cancelled records.
	Generation, Timeline, Seq int64
	Positioned                bool
	// Order is where it was read (the evidence store's sequence, then the alert channel's): intent-only records of
	// one owner go in it.
	Order int
}

// ParseEvidence reads a record's identity, owner, category, phase, subject and position.
func ParseEvidence(raw []byte, order int) (Evidence, error) {
	var r struct {
		RecordType string `json:"record_type"`
		ID         string `json:"id"`
		Recorder   struct {
			Component string `json:"component"`
		} `json:"recorder"`
		Category string `json:"category"`
		Phase    string `json:"phase"`
		Subject  string `json:"subject"`
		Position *struct {
			Epoch     string `json:"epoch"`
			CommitSeq int64  `json:"commit_seq"`
		} `json:"position"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Evidence{}, fmt.Errorf("not JSON: %w", err)
	}
	if r.RecordType != "be.evidence.record" || r.ID == "" || r.Recorder.Component == "" || r.Phase == "" {
		return Evidence{}, errors.New("not a be.evidence.record")
	}
	e := Evidence{Raw: raw, ID: r.ID, Owner: r.Recorder.Component, Category: r.Category, Phase: r.Phase,
		Subject: r.Subject, Order: order}
	if r.Position != nil {
		g, t, ok := strings.Cut(r.Position.Epoch, ".")
		gen, err1 := strconv.ParseInt(g, 10, 64)
		tl, err2 := strconv.ParseInt(t, 10, 64)
		if !ok || err1 != nil || err2 != nil {
			return Evidence{}, fmt.Errorf("record %s: the epoch %q is not <generation>.<timeline>", r.ID, r.Position.Epoch)
		}
		e.Generation, e.Timeline, e.Seq, e.Positioned = gen, tl, r.Position.CommitSeq, true
	}
	return e, nil
}

// Planned is one record to replay.
type Planned struct {
	Evidence
	// IntentOnly: no conclusion was ever written (protocol 15: executed after the owner's others, marked
	// "intent unconfirmed" for review).
	IntentOnly bool
}

// Ignored is a record the replay leaves, and why.
type Ignored struct {
	ID     string `json:"id"`
	Owner  string `json:"owner"`
	Reason string `json:"reason"`
}

// Plan is the replay's order: Owners in turn (every owner but the authorization member, by owner, each in its order),
// then Authz (its effective and general records), then AuthzDeferred (its intent-only tightenings, after 6.4).
type Plan struct {
	Owners        []Planned
	Authz         []Planned
	AuthzDeferred []Planned
	Ignored       []Ignored
}

// PlanReplay merges the records by identity and orders them.
func PlanReplay(records []Evidence) Plan {
	byID := map[string]map[string]Evidence{}
	var ids []string
	for _, r := range records {
		if byID[r.ID] == nil {
			byID[r.ID] = map[string]Evidence{}
			ids = append(ids, r.ID)
		}
		if _, ok := byID[r.ID][r.Phase]; !ok { // the first read of an (id, phase) is kept: the store's before the alert's
			byID[r.ID][r.Phase] = r
		}
	}
	var p Plan
	groups := map[string][]Planned{}
	for _, id := range ids {
		phases := byID[id]
		var e Evidence
		intentOnly := false
		switch {
		case has(phases, "cancelled"):
			r := any1(phases)
			p.Ignored = append(p.Ignored, Ignored{ID: id, Owner: r.Owner, Reason: "cancelled"})
			continue
		case any1(phases).Category == DoubleFailureCategory: // P12.23: what it names was never stored
			p.Ignored = append(p.Ignored, Ignored{ID: id, Owner: any1(phases).Owner, Reason: "a double failure: listed in step 9"})
			continue
		case has(phases, "effective"):
			e = phases["effective"]
		case has(phases, "general"):
			e = phases["general"]
		case has(phases, "intent"):
			e, intentOnly = phases["intent"], true
		default:
			r := any1(phases)
			p.Ignored = append(p.Ignored, Ignored{ID: id, Owner: r.Owner, Reason: "phase " + r.Phase + " is not replayed"})
			continue
		}
		groups[e.Owner] = append(groups[e.Owner], Planned{Evidence: e, IntentOnly: intentOnly})
	}
	owners := make([]string, 0, len(groups))
	for o := range groups {
		owners = append(owners, o)
	}
	sort.Strings(owners)
	for _, o := range owners {
		g := groups[o]
		sort.SliceStable(g, func(i, j int) bool { return before(g[i], g[j]) })
		if o != AuthzID {
			p.Owners = append(p.Owners, g...)
			continue
		}
		for _, r := range g {
			if r.IntentOnly && r.Category == "security_tightening" {
				p.AuthzDeferred = append(p.AuthzDeferred, r)
			} else {
				p.Authz = append(p.Authz, r)
			}
		}
	}
	sort.SliceStable(p.Ignored, func(i, j int) bool { return p.Ignored[i].ID < p.Ignored[j].ID })
	return p
}

// before orders one owner's records: positioned ones by (generation, timeline, sequence), the intent-only after them
// in the order they were read.
func before(a, b Planned) bool {
	switch {
	case a.IntentOnly != b.IntentOnly:
		return !a.IntentOnly
	case a.IntentOnly:
		return a.Order < b.Order
	case a.Generation != b.Generation:
		return a.Generation < b.Generation
	case a.Timeline != b.Timeline:
		return a.Timeline < b.Timeline
	}
	return a.Seq < b.Seq
}

func has(m map[string]Evidence, phase string) bool { _, ok := m[phase]; return ok }

// any1 is one of a record's phases (a deterministic one: the smallest phase name).
func any1(m map[string]Evidence) Evidence {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return m[keys[0]]
}

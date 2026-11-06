package recovery

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// The planning of steps 7, 9 and 10 (assembly protocol 15; be-protocol chapters 13 and 14): pure functions over what
// the evidence store, the audit store and the members answered, so that the calls (VerifyCalls) stay thin.

// IssuedCategory is the evidence category of an irrevocable external command's issue record (be-protocol P13.4).
const IssuedCategory = "external_command_issued"

// IssuedAfter are the business references of the issue records written at or after t, by recorder: what an adapter
// may have sent after the recovery point (step 7, P13.11). Records it cannot read are skipped (step 6 listed them).
func IssuedAfter(records [][]byte, t time.Time) map[string][]string {
	out := map[string][]string{}
	for _, raw := range records {
		var r struct {
			RecordType string `json:"record_type"`
			Category   string `json:"category"`
			At         string `json:"at"`
			Recorder   struct {
				Component string `json:"component"`
			} `json:"recorder"`
			Change struct {
				Reference string `json:"reference"`
			} `json:"change"`
		}
		if json.Unmarshal(raw, &r) != nil || r.RecordType != "be.evidence.record" || r.Category != IssuedCategory ||
			r.Change.Reference == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, r.At)
		if err != nil || at.Before(t) {
			continue
		}
		if !contains(out[r.Recorder.Component], r.Change.Reference) {
			out[r.Recorder.Component] = append(out[r.Recorder.Component], r.Change.Reference)
		}
	}
	for c := range out {
		sort.Strings(out[c])
	}
	return out
}

// DoubleFailureCategory is the evidence category of the record a double failure leaves (be-protocol P12.23).
const DoubleFailureCategory = "evidence_double_failure"

// EvidenceLoss is a tightening neither the evidence store nor the alert channel took (a double failure): the replay
// cannot apply it, so it is a declared loss whose subject and object a person reviews (protocol 15, step 9, RC22).
type EvidenceLoss struct {
	Recorder   string `json:"recorder"`
	Record     string `json:"record"`   // the lost tightening's identity
	Category   string `json:"category"` // and its category
	Subject    string `json:"subject,omitempty"`
	ObjectType string `json:"object_type,omitempty"`
	ObjectID   string `json:"object_id,omitempty"`
	At         string `json:"at"`
}

// DoubleFailures are the double failures at or after t, in time order (those before t are in the restored databases'
// own history).
func DoubleFailures(records [][]byte, t time.Time) []EvidenceLoss {
	out := []EvidenceLoss{}
	for _, raw := range records {
		var r struct {
			RecordType string `json:"record_type"`
			Category   string `json:"category"`
			At         string `json:"at"`
			Subject    string `json:"subject"`
			Recorder   struct {
				Component string `json:"component"`
			} `json:"recorder"`
			Object struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"object"`
			Change struct {
				Record   string `json:"record"`
				Category string `json:"category"`
			} `json:"change"`
		}
		if json.Unmarshal(raw, &r) != nil || r.RecordType != "be.evidence.record" || r.Category != DoubleFailureCategory {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, r.At)
		if err != nil || at.Before(t) {
			continue
		}
		out = append(out, EvidenceLoss{Recorder: r.Recorder.Component, Record: r.Change.Record, Category: r.Change.Category,
			Subject: r.Subject, ObjectType: r.Object.Type, ObjectID: r.Object.ID, At: r.At})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// AuditRecord is one audit record of the audit store, as the loss and the numbers read it.
type AuditRecord struct {
	Source      string // the component that wrote it: the owner of what it names
	SourceEpoch string
	SourceSeq   int64
	ID          string
	OccurredAt  time.Time
	Category    string
	Operation   string
	Actor       string
	TargetType  string
	TargetID    string
	Version     string          // the target's version after the change; empty when it names none
	Number      *DocumentNumber // the number a continuous series issued (P14.5), when it names one
}

// DocumentNumber is a business change's changes.document_number (P14.5).
type DocumentNumber struct {
	LegalEntity string `json:"legal_entity"`
	Code        string `json:"code"`
	Period      string `json:"period"`
	Number      string `json:"number"`
}

// ParseAudit reads a stored audit record (be.audit.record).
func ParseAudit(source, epoch string, seq int64, raw []byte) (AuditRecord, error) {
	var r struct {
		ID         string         `json:"id"`
		OccurredAt string         `json:"occurred_at"`
		Category   string         `json:"category"`
		Operation  string         `json:"operation"`
		Actor      string         `json:"actor"`
		Target     map[string]any `json:"target"`
		Changes    struct {
			DocumentNumber *DocumentNumber `json:"document_number"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return AuditRecord{}, err
	}
	at, err := time.Parse(time.RFC3339Nano, r.OccurredAt)
	if err != nil {
		return AuditRecord{}, fmt.Errorf("occurred_at: %w", err)
	}
	str := func(k string) string { s, _ := r.Target[k].(string); return s }
	return AuditRecord{Source: source, SourceEpoch: epoch, SourceSeq: seq, ID: r.ID, OccurredAt: at, Category: r.Category,
		Operation: r.Operation, Actor: r.Actor, TargetType: str("type"), TargetID: str("id"), Version: str("version"),
		Number: r.Changes.DocumentNumber}, nil
}

// Asked is one object the loss asks its owner about (P14.3): the latest version any audit record after T names.
type Asked struct {
	Owner, Type, ID, Version string
	Records                  []AuditRecord // the records that named it, in the order they occurred
}

// LossCategories are the audit categories step 9 asks the owners about: a business change or a sensitive read may be
// gone with the recovery. A security change is not asked: step 6 replays it from the evidence.
var LossCategories = []string{"business_change", "sensitive_read"}

// AskOwners groups the audit records after T by owner and object: one question per object, at the highest version
// named (a later change of the same object implies the earlier ones). Records without an object identity are left out
// (an operation on a collection, a configuration); they are counted in the report.
func AskOwners(records []AuditRecord) (asked []Asked, unnamed int) {
	byKey := map[string]*Asked{}
	var keys []string
	for _, r := range records {
		if !contains(LossCategories, r.Category) {
			continue
		}
		if r.TargetType == "" || r.TargetID == "" {
			unnamed++
			continue
		}
		k := r.Source + "\x00" + r.TargetType + "\x00" + r.TargetID
		a := byKey[k]
		if a == nil {
			a = &Asked{Owner: r.Source, Type: r.TargetType, ID: r.TargetID}
			byKey[k] = a
			keys = append(keys, k)
		}
		a.Records = append(a.Records, r)
		if higher(r.Version, a.Version) {
			a.Version = r.Version
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := byKey[k]
		sort.SliceStable(a.Records, func(i, j int) bool { return a.Records[i].OccurredAt.Before(a.Records[j].OccurredAt) })
		asked = append(asked, *a)
	}
	return asked, unnamed
}

// higher reports whether version v is above w (decimal revisions; an empty one is below any).
func higher(v, w string) bool {
	a, errA := strconv.ParseInt(v, 10, 64)
	b, errB := strconv.ParseInt(w, 10, 64)
	switch {
	case errA != nil:
		return false
	case errB != nil:
		return true
	}
	return a > b
}

// The loss list's verdicts on one object (step 9).
const (
	LossLost       = "lost"       // absent, or older than the change the audit names: re-enter it
	LossUnprovable = "unprovable" // present but the audit names no version, or the owner cannot answer for its type
)

// Lost is one entry of the loss list: an object a user changed or read after T that the recovery did not keep.
type Lost struct {
	Owner      string   `json:"owner"`
	Type       string   `json:"type"`
	ID         string   `json:"id"`
	Verdict    string   `json:"verdict"`
	Answer     string   `json:"answer"`  // the owner's: present, older, absent, or why it could not answer
	Asked      string   `json:"asked"`   // the version the audit names
	Version    string   `json:"version"` // the owner's version now
	Users      []string `json:"users"`   // who did it
	Operations []string `json:"operations"`
	Since      string   `json:"since"` // the first such record's time
}

// Judge turns the owner's answer about an object into a loss entry, or none when nothing was lost.
func Judge(a Asked, answer, version string) *Lost {
	l := &Lost{Owner: a.Owner, Type: a.Type, ID: a.ID, Answer: answer, Asked: a.Version, Version: version,
		Users: []string{}, Operations: []string{}} // the run profile's canonical encoding has no null
	switch {
	case answer == "older" || answer == "absent":
		l.Verdict = LossLost
	case answer == "present" && a.Version == "":
		l.Verdict = LossUnprovable
	case answer == "present":
		return nil
	default:
		l.Verdict = LossUnprovable
	}
	for _, r := range a.Records {
		if r.Actor != "" && !contains(l.Users, r.Actor) {
			l.Users = append(l.Users, r.Actor)
		}
		if !contains(l.Operations, r.Operation) {
			l.Operations = append(l.Operations, r.Operation)
		}
	}
	if len(a.Records) > 0 {
		l.Since = a.Records[0].OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	return l
}

// SourceLoss is the audit loss of one source in one of its epochs (be-protocol P14.4; protocol 03, "backlog and
// failure"): the production sequences missing between the first and the last the store has after T, and the window
// after the last one until the failure, in which records the source produced may never have reached the store.
type SourceLoss struct {
	Source   string  `json:"source"`
	Epoch    string  `json:"epoch"`
	Gaps     [][]int `json:"gaps"` // [from, to] inclusive
	LastSeq  int64   `json:"last_seq"`
	LastAt   string  `json:"last_at"`
	WindowTo string  `json:"window_to"` // the failure moment
}

// AuditLoss finds each source's gaps in the records listed (all categories, after T).
func AuditLoss(records []AuditRecord, failedAt time.Time) []SourceLoss {
	type key struct{ source, epoch string }
	seqs := map[key][]AuditRecord{}
	var keys []key
	for _, r := range records {
		k := key{r.Source, r.SourceEpoch}
		if _, ok := seqs[k]; !ok {
			keys = append(keys, k)
		}
		seqs[k] = append(seqs[k], r)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].source < keys[j].source || keys[i].source == keys[j].source && keys[i].epoch < keys[j].epoch
	})
	var out []SourceLoss
	for _, k := range keys {
		rs := seqs[k]
		sort.Slice(rs, func(i, j int) bool { return rs[i].SourceSeq < rs[j].SourceSeq })
		l := SourceLoss{Source: k.source, Epoch: k.epoch, Gaps: [][]int{}, LastSeq: rs[len(rs)-1].SourceSeq,
			LastAt: rs[len(rs)-1].OccurredAt.UTC().Format(time.RFC3339Nano), WindowTo: failedAt.UTC().Format(time.RFC3339Nano)}
		for i := 1; i < len(rs); i++ {
			if rs[i].SourceSeq > rs[i-1].SourceSeq+1 {
				l.Gaps = append(l.Gaps, []int{int(rs[i-1].SourceSeq + 1), int(rs[i].SourceSeq - 1)})
			}
		}
		out = append(out, l)
	}
	return out
}

// Series is a continuous document number series and a used maximum.
type Series struct {
	Owner       string `json:"owner"`
	LegalEntity string `json:"legal_entity"`
	Code        string `json:"code"`
	Period      string `json:"period"`
	Max         int64  `json:"max"`
	FromAudit   int64  `json:"from_audit"` // the highest the audit after T names; 0: none
}

// RaisePlan is step 10's numbers (P14.5): for every series an owner answers and every series an audit record after T
// names, the greater of the two; only the series the audit raises above the owner's are to be advanced.
func RaisePlan(owned []Series, records []AuditRecord) (all, raise []Series) {
	byKey := map[string]*Series{}
	var keys []string
	key := func(owner, le, code, period string) string {
		return owner + "\x00" + le + "\x00" + code + "\x00" + period
	}
	for _, s := range owned {
		k := key(s.Owner, s.LegalEntity, s.Code, s.Period)
		c := s
		byKey[k] = &c
		keys = append(keys, k)
	}
	for _, r := range records {
		if r.Number == nil {
			continue
		}
		n, err := strconv.ParseInt(r.Number.Number, 10, 64)
		if err != nil {
			continue
		}
		k := key(r.Source, r.Number.LegalEntity, r.Number.Code, r.Number.Period)
		s := byKey[k]
		if s == nil {
			s = &Series{Owner: r.Source, LegalEntity: r.Number.LegalEntity, Code: r.Number.Code, Period: r.Number.Period}
			byKey[k] = s
			keys = append(keys, k)
		}
		s.FromAudit = max(s.FromAudit, n)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := *byKey[k]
		all = append(all, s)
		if s.FromAudit > s.Max {
			s.Max = s.FromAudit
			raise = append(raise, s)
		}
	}
	return all, raise
}

// Package recovery is be-ops recover: the tool of a full recovery (assembly protocol 15, "the recovery flow"). The
// deployment's recovery state lives in the run profile (be-protocol chapter 12, kind recovery.step): every step the
// tool takes appends a signed record, and the state is those records folded in order, so it survives the very
// databases the recovery puts back.
package recovery

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// The deployment's states during a recovery (protocol 15, the state table).
const (
	Running   = "running"
	Frozen    = "frozen"
	Restoring = "restoring"
	Verifying = "verifying" // steps 7-9
	ReadOnly  = "read_only" // open for reading
)

// The steps (protocol 15): 1 announce, 2 freeze, 3 restore-db, 4 advance, 5 redeliver, 6 replay (6.2-6.6) and keys
// (6.8), 7 reconcile, 8 check, 9 loss, 10 open-read and open; abandon leaves before step 3.
const (
	StepAnnounce  = "announce"
	StepAbandon   = "abandon"
	StepFreeze    = "freeze"
	StepRestoreDB = "restore-db"
	StepAdvance   = "advance"
	StepRedeliver = "redeliver"
	StepReplay    = "replay"
	StepKeys      = "keys"
	StepReconcile = "reconcile"
	StepCheck     = "check"
	StepLoss      = "loss"
	StepOpenRead  = "open-read"
	StepOpen      = "open"
)

// AtLayout is the fixed width a step's time is written in, so the records order as strings (the run profile's
// written_at has the same form, P12.1).
const AtLayout = "2006-01-02T15:04:05.000000Z"

// Step is one recovery.step record's body.
type Step struct {
	Step     string         `json:"step"`
	State    string         `json:"state"`              // the deployment's state after the step
	Recovery string         `json:"recovery,omitempty"` // the recovery it belongs to: the announcement's T and time
	T        string         `json:"t,omitempty"`        // the point in time the databases go back to (announce)
	Reason   string         `json:"reason,omitempty"`
	Approver string         `json:"approver,omitempty"`
	Detail   map[string]any `json:"detail,omitempty"` // what the step did (schemas copied back, generations, ...)
	At       string         `json:"at"`               // AtLayout, UTC
}

// Status is the deployment's recovery state: the folded records.
type Status struct {
	State    string
	Recovery string    // the current recovery (empty when running)
	T        time.Time // its point in time
	Done     []string  // the steps done in the current recovery, in order (repeated steps once)
}

// Fold reads the records in the order they were written (by their at, then their order) into the current status.
func Fold(steps []Step) (Status, error) {
	sorted := append([]Step(nil), steps...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At < sorted[j].At })
	st := Status{State: Running}
	for _, s := range sorted {
		if err := Allowed(st, s.Step); err != nil {
			return st, fmt.Errorf("the run profile's recovery records do not fold (%s at %s): %w", s.Step, s.At, err)
		}
		st = apply(st, s)
	}
	return st, nil
}

func apply(st Status, s Step) Status {
	switch s.Step {
	case StepAnnounce:
		t, _ := time.Parse(time.RFC3339Nano, s.T)
		return Status{State: Frozen, Recovery: s.Recovery, T: t}
	case StepAbandon, StepOpen:
		return Status{State: Running}
	}
	st.State = s.State
	for _, d := range st.Done {
		if d == s.Step {
			return st
		}
	}
	st.Done = append(st.Done, s.Step)
	return st
}

// Allowed checks a step against the state table: announce from running; abandon while frozen and before any database
// is put back; freeze while frozen (or again while restoring); restore-db, advance and redeliver while restoring, each
// after the one before it. A step may be repeated (protocol 15: "each step may be done again").
func Allowed(st Status, step string) error {
	done := func(s string) bool {
		for _, d := range st.Done {
			if d == s {
				return true
			}
		}
		return false
	}
	switch step {
	case StepAnnounce:
		if st.State != Running {
			return fmt.Errorf("a recovery is under way (%s)", st.State)
		}
	case StepAbandon:
		if st.State != Frozen {
			return fmt.Errorf("only a frozen deployment is given up on, not %s: once the databases go back it goes on", st.State)
		}
	case StepFreeze:
		if st.State != Frozen && st.State != Restoring {
			return fmt.Errorf("announce first (the deployment is %s)", st.State)
		}
	case StepRestoreDB:
		if st.State != Restoring {
			return fmt.Errorf("freeze first (the deployment is %s)", st.State)
		}
	case StepAdvance:
		if st.State != Restoring || !done(StepRestoreDB) {
			return fmt.Errorf("restore the databases first")
		}
	case StepRedeliver:
		if st.State != Restoring || !done(StepAdvance) {
			return fmt.Errorf("advance the generations first")
		}
	case StepReplay:
		if st.State != Restoring || !done(StepRedeliver) {
			return fmt.Errorf("redeliver first")
		}
	case StepKeys:
		if st.State != Restoring || !done(StepReplay) {
			return fmt.Errorf("replay first")
		}
	case StepReconcile: // steps 3-6 done: verifying (again while verifying)
		if !(st.State == Restoring && done(StepKeys)) && st.State != Verifying {
			return fmt.Errorf("finish step 6 first (replay and keys; the deployment is %s)", st.State)
		}
	case StepCheck, StepLoss:
		if st.State != Verifying || !done(StepReconcile) {
			return fmt.Errorf("reconcile first (step 7)")
		}
	case StepOpenRead:
		if st.State != Verifying || !done(StepCheck) || !done(StepLoss) {
			return fmt.Errorf("check and list the loss first (steps 8 and 9)")
		}
	case StepOpen:
		if st.State != ReadOnly {
			return fmt.Errorf("open for reading first (the deployment is %s)", st.State)
		}
	default:
		return fmt.Errorf("no such step: %s", step)
	}
	return nil
}

// After is the state a step leaves the deployment in.
func After(step string) string {
	switch step {
	case StepAnnounce:
		return Frozen
	case StepAbandon, StepOpen:
		return Running
	case StepReconcile, StepCheck, StepLoss:
		return Verifying
	case StepOpenRead:
		return ReadOnly
	}
	return Restoring
}

// Decode reads a record's body.
func Decode(body json.RawMessage) (Step, error) {
	var s Step
	err := json.Unmarshal(body, &s)
	return s, err
}

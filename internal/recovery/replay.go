package recovery

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// The replay's calls (assembly protocol 15, 6.3-6.5): every planned record to its owner's ReplayService, in the plan's
// order; then the authorization member's RecoveryService: Sources until the sources' replay is applied (6.4), its
// deferred intent-only tightenings, the invariant check with the subjects under review (6.5). A record whose
// re-execution fails is not skipped silently: its subject is closed for review (the check), the record listed.

// Outcomes besides the SDK's (reflected, applied, dangling, unsupported) and the authorization member's (for_review).
const (
	OutcomeFailed        = "failed"        // the owner answered an error: the record is listed, its subject closed
	OutcomeUnimplemented = "unimplemented" // the owner has no ReplayService (an older version): listed for review
)

// Caller is what the replay calls.
type Caller interface {
	// Replay is an owner's ReplayService. err is a failure to reach it (the step stops, to be done again); an answer
	// that is an error comes back as the outcome failed (or unimplemented) with its detail.
	Replay(ctx context.Context, owner string, record []byte) (outcome, detail string, err error)
	Sources(ctx context.Context, since string) (asOf string, applied bool, err error)
	ReplayDeferred(ctx context.Context, record []byte) (outcome, detail string, err error)
	Check(ctx context.Context, reviewSubjects []string) (CheckResult, error)
}

// CheckResult is the authorization member's invariant check (its CheckResponse).
type CheckResult struct {
	AdminState      string   `json:"admin_state"`
	Ended           int32    `json:"ended"`
	Held            int32    `json:"held"`
	Review          int32    `json:"review"`
	Closed          []string `json:"closed,omitempty"`
	AdminsNotClosed []string `json:"admins_not_closed,omitempty"`
	Unprovable      []string `json:"unprovable,omitempty"`
}

// Outcome is one record's answer.
type Outcome struct {
	ID         string `json:"id"`
	Owner      string `json:"owner"`
	Category   string `json:"category"`
	Phase      string `json:"phase"`
	Outcome    string `json:"outcome"`
	Detail     string `json:"detail,omitempty"`
	IntentOnly bool   `json:"intent_only,omitempty"` // "intent unconfirmed": for review whatever the outcome
}

// Report is the replay's list (what the step records in the run profile).
type Report struct {
	Outcomes []Outcome   `json:"outcomes"`
	Ignored  []Ignored   `json:"ignored,omitempty"`
	SourceAt string      `json:"sources_applied_at"`
	Check    CheckResult `json:"check"`
	// Review are the records that need a person: failed, dangling, unsupported, unimplemented, for review, or
	// intent-only (their ids).
	Review []string `json:"review,omitempty"`
}

// RunReplay makes the calls of a plan. review are the subjects already under review (6.2: a signature that does not
// hold); wait bounds the wait for 6.4; sleep is time.Sleep in production.
func RunReplay(ctx context.Context, p Plan, c Caller, review []string, wait time.Duration, sleep func(time.Duration)) (Report, error) {
	rep := Report{Ignored: p.Ignored}
	subjects := map[string]bool{}
	for _, s := range review {
		subjects[s] = true
	}
	note := func(r Planned, outcome, detail string) {
		rep.Outcomes = append(rep.Outcomes, Outcome{ID: r.ID, Owner: r.Owner, Category: r.Category, Phase: r.Phase,
			Outcome: outcome, Detail: detail, IntentOnly: r.IntentOnly})
		switch outcome {
		case "applied", "reflected":
			if r.IntentOnly {
				rep.Review = append(rep.Review, r.ID)
			}
		default:
			rep.Review = append(rep.Review, r.ID)
			if (outcome == OutcomeFailed || outcome == OutcomeUnimplemented) && r.Subject != "" {
				subjects[r.Subject] = true // a re-execution that failed: its subject closed (protocol 15)
			}
		}
	}
	for _, r := range append(append([]Planned{}, p.Owners...), p.Authz...) {
		outcome, detail, err := c.Replay(ctx, r.Owner, r.Raw)
		if err != nil {
			return rep, fmt.Errorf("replaying %s at %s: %w (do the step again: what was done answers reflected)", r.ID, r.Owner, err)
		}
		note(r, outcome, detail)
	}
	asOf, applied, err := c.Sources(ctx, "")
	if err != nil {
		return rep, fmt.Errorf("6.4: %w", err)
	}
	for deadline := time.Now().Add(wait); !applied; {
		if time.Now().After(deadline) {
			return rep, fmt.Errorf("6.4: the authorization member has not applied its sources' replay within %s", wait)
		}
		sleep(time.Second)
		if _, applied, err = c.Sources(ctx, asOf); err != nil {
			return rep, fmt.Errorf("6.4: %w", err)
		}
	}
	rep.SourceAt = asOf
	for _, r := range p.AuthzDeferred {
		outcome, detail, err := c.ReplayDeferred(ctx, r.Raw)
		if err != nil {
			return rep, fmt.Errorf("6.5: the deferred %s: %w", r.ID, err)
		}
		note(r, outcome, detail)
	}
	list := make([]string, 0, len(subjects))
	for s := range subjects {
		list = append(list, s)
	}
	sort.Strings(list)
	if rep.Check, err = c.Check(ctx, list); err != nil {
		return rep, fmt.Errorf("6.5: the invariant check: %w", err)
	}
	if len(rep.Check.Unprovable) > 0 {
		return rep, fmt.Errorf("6.5: the accounts of %v cannot be proven now; nothing was judged: do the step again", rep.Check.Unprovable)
	}
	return rep, nil
}

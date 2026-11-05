package recovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/brickKit/be-sdk-go/v3/externalpb"
	"github.com/brickKit/be-sdk-go/v3/recoverypb"
	auditv3 "github.com/brickKit/infra-audit/contract/v3/gen/go/be/audit/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The calls of steps 7-10 in the deployment's network (like step 6's, VerifyCalls runs in a container of the tool's
// own binary): the external reconciliation, the checks, the loss and the numbers (be-protocol chapters 13 and 14).

// The kinds of VerifyInput.
const (
	CallsReconcile = "reconcile"
	CallsCheck     = "check"
	CallsLoss      = "loss"
	CallsNumbers   = "numbers"
)

// verifyCallLimit bounds one call of steps 7-10 (a reconciliation lists or queries an external system).
const verifyCallLimit = 10 * time.Minute

// VerifyInput is what the calls get from a step.
type VerifyInput struct {
	Kind      string            `json:"kind"`
	Recovery  string            `json:"recovery"`
	T         string            `json:"t"`         // the recovery point, RFC 3339
	FailedAt  string            `json:"failed_at"` // the failure moment; empty: the call's time
	Window    int64             `json:"window_before_seconds"`
	Addresses map[string]string `json:"addresses"`
	Members   []string          `json:"members"` // the members with a database (CheckService)
	Senders   []string          `json:"senders"` // the members that send external commands (ReconcileService)
	Raise     bool              `json:"raise"`   // numbers: advance the series (else only report)
}

// Conclusion is one reconciliation conclusion (P13.13).
type Conclusion struct {
	Reference      string `json:"reference"`
	Command        string `json:"command,omitempty"`
	Conclusion     string `json:"conclusion"`
	State          string `json:"state,omitempty"`
	ResendDeadline string `json:"resend_deadline,omitempty"`
	Detail         string `json:"detail,omitempty"`
}

// CheckOutcome is one member's check (P14.1).
type CheckOutcome struct {
	Check       string   `json:"check"`
	Basis       string   `json:"basis"`
	Outcome     string   `json:"outcome"`
	Differences []string `json:"differences"`
	Disposal    string   `json:"disposal,omitempty"`
}

// VerifyReport is what the calls answer; the fields of the kind are set.
type VerifyReport struct {
	Conclusions   map[string][]Conclusion   `json:"conclusions,omitempty"`   // reconcile, by adapter
	Checks        map[string][]CheckOutcome `json:"checks,omitempty"`        // check, by member
	Unimplemented []string                  `json:"unimplemented,omitempty"` // members without the standard method
	Failed        map[string]string         `json:"failed,omitempty"`        // members that answered an error
	Lost          []Lost                    `json:"lost,omitempty"`          // loss
	Asked         int                       `json:"asked,omitempty"`         // loss: objects asked
	Unnamed       int                       `json:"unnamed,omitempty"`       // loss: records naming no object
	AuditLoss     []SourceLoss              `json:"audit_loss,omitempty"`    // loss
	Series        []Series                  `json:"series,omitempty"`        // numbers: every series
	Raised        []Series                  `json:"raised,omitempty"`        // numbers: those the audit raises
	Pass          bool                      `json:"pass"`                    // check: every check passed
}

// VerifyCaller is what the calls need of the members.
type VerifyCaller interface {
	ListEvidence(ctx context.Context) ([][]byte, error)
	ListAudit(ctx context.Context, since time.Time, categories []string) ([]AuditRecord, error)
	Reconcile(ctx context.Context, adapter string, req *externalpb.ReconcileRequest) ([]Conclusion, error)
	MemberChecks(ctx context.Context, member string) ([]CheckOutcome, error)
	Present(ctx context.Context, owner string, objs []Asked) (map[string][2]string, error) // id\x00type -> answer, version
	Numbers(ctx context.Context, owner string) ([]Series, error)
	Advance(ctx context.Context, owner string, series []Series) error
}

// unimplemented reports a member that answers no standard method of chapters 13 and 14 (an older version).
func unimplemented(err error) bool { return status.Code(err) == codes.Unimplemented }

// VerifyCalls runs one kind of calls.
func VerifyCalls(ctx context.Context, c VerifyCaller, in VerifyInput) (VerifyReport, error) {
	t, err := time.Parse(time.RFC3339Nano, in.T)
	if err != nil {
		return VerifyReport{}, fmt.Errorf("T: %w", err)
	}
	failedAt := time.Now().UTC()
	if in.FailedAt != "" {
		if failedAt, err = time.Parse(time.RFC3339Nano, in.FailedAt); err != nil {
			return VerifyReport{}, fmt.Errorf("the failure moment: %w", err)
		}
	}
	rep := VerifyReport{Failed: map[string]string{}}
	switch in.Kind {
	case CallsReconcile:
		err = reconcileCalls(ctx, c, in, t, &rep)
	case CallsCheck:
		err = checkCalls(ctx, c, in, &rep)
	case CallsLoss:
		err = lossCalls(ctx, c, t, failedAt, &rep)
	case CallsNumbers:
		err = numberCalls(ctx, c, in, t, &rep)
	default:
		err = fmt.Errorf("no such calls: %s", in.Kind)
	}
	return rep, err
}

// reconcileCalls is step 7: every adapter reconciles, given its issue records after T.
func reconcileCalls(ctx context.Context, c VerifyCaller, in VerifyInput, t time.Time, rep *VerifyReport) error {
	stored, err := c.ListEvidence(ctx)
	if err != nil {
		return err
	}
	issued := IssuedAfter(stored, t)
	rep.Conclusions = map[string][]Conclusion{}
	for _, a := range in.Senders {
		got, err := c.Reconcile(ctx, a, &externalpb.ReconcileRequest{Recovery: in.Recovery, T: in.T,
			WindowBeforeSeconds: in.Window, Issued: issued[a], FailedAt: in.FailedAt})
		switch {
		case unimplemented(err):
			rep.Unimplemented = append(rep.Unimplemented, a)
		case err != nil:
			rep.Failed[a] = err.Error()
		default:
			rep.Conclusions[a] = got
		}
	}
	if len(rep.Failed) > 0 {
		return fmt.Errorf("the reconciliation of %d adapters failed (redo the step): %v", len(rep.Failed), rep.Failed)
	}
	return nil
}

// checkCalls is step 8: every member's checks; the step passes when every check of every member passed.
func checkCalls(ctx context.Context, c VerifyCaller, in VerifyInput, rep *VerifyReport) error {
	rep.Checks, rep.Pass = map[string][]CheckOutcome{}, true
	for _, m := range in.Members {
		got, err := c.MemberChecks(ctx, m)
		switch {
		case unimplemented(err):
			rep.Unimplemented, rep.Pass = append(rep.Unimplemented, m), false
		case err != nil:
			rep.Failed[m], rep.Pass = err.Error(), false
		default:
			rep.Checks[m] = got
			for _, o := range got {
				if o.Outcome != "pass" {
					rep.Pass = false
				}
			}
		}
	}
	return nil
}

// lossCalls is step 9: the audit records after T asked of their owners (P14.3), and the audit loss (P14.4).
func lossCalls(ctx context.Context, c VerifyCaller, t, failedAt time.Time, rep *VerifyReport) error {
	records, err := c.ListAudit(ctx, t, nil)
	if err != nil {
		return err
	}
	rep.AuditLoss = AuditLoss(records, failedAt)
	asked, unnamed := AskOwners(records)
	rep.Asked, rep.Unnamed = len(asked), unnamed
	byOwner := map[string][]Asked{}
	var owners []string
	for _, a := range asked {
		if _, ok := byOwner[a.Owner]; !ok {
			owners = append(owners, a.Owner)
		}
		byOwner[a.Owner] = append(byOwner[a.Owner], a)
	}
	sort.Strings(owners)
	rep.Lost = []Lost{}
	for _, o := range owners {
		answers, err := c.Present(ctx, o, byOwner[o])
		for _, a := range byOwner[o] {
			answer, version := "", ""
			switch {
			case err != nil:
				answer = errorReason(err)
			default:
				got := answers[a.ID+"\x00"+a.Type]
				answer, version = got[0], got[1]
			}
			if l := Judge(a, answer, version); l != nil {
				rep.Lost = append(rep.Lost, *l)
			}
		}
	}
	return nil
}

// errorReason is a short reason for an owner's error in the loss list.
func errorReason(err error) string {
	if s, ok := status.FromError(err); ok {
		return s.Code().String() + ": " + s.Message()
	}
	return err.Error()
}

// numberCalls is step 10's numbers: every member's continuous series and the audit's issued numbers after T; with
// Raise, the series the audit raises are advanced by their owners (P14.5).
func numberCalls(ctx context.Context, c VerifyCaller, in VerifyInput, t time.Time, rep *VerifyReport) error {
	var owned []Series
	for _, m := range in.Members {
		got, err := c.Numbers(ctx, m)
		switch {
		case unimplemented(err):
			rep.Unimplemented = append(rep.Unimplemented, m)
		case err != nil:
			return fmt.Errorf("%s's numbers: %w", m, err)
		default:
			owned = append(owned, got...)
		}
	}
	records, err := c.ListAudit(ctx, t, []string{"business_change"})
	if err != nil {
		return err
	}
	rep.Series, rep.Raised = RaisePlan(owned, records)
	if !in.Raise {
		return nil
	}
	byOwner := map[string][]Series{}
	for _, s := range rep.Raised {
		byOwner[s.Owner] = append(byOwner[s.Owner], s)
	}
	var errsOf []error
	for owner, series := range byOwner {
		if err := c.Advance(ctx, owner, series); err != nil {
			errsOf = append(errsOf, fmt.Errorf("%s: %w", owner, err))
		}
	}
	return errors.Join(errsOf...)
}

// The members' standard methods over gRPC (GRPCCaller): chapters 13 and 14, and the audit store's records.

func (g *GRPCCaller) Reconcile(ctx context.Context, adapter string, req *externalpb.ReconcileRequest) ([]Conclusion, error) {
	c, err := g.conn(adapter)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, verifyCallLimit)
	defer cancel()
	out, err := externalpb.NewReconcileServiceClient(c).Reconcile(ctx, req)
	if err != nil {
		return nil, err
	}
	conclusions := []Conclusion{}
	for _, x := range out.GetConclusions() {
		conclusions = append(conclusions, Conclusion{Reference: x.GetReference(), Command: x.GetCommand(), Conclusion: x.GetConclusion(),
			State: x.GetState(), ResendDeadline: x.GetResendDeadline(), Detail: x.GetDetail()})
	}
	return conclusions, nil
}

func (g *GRPCCaller) checks(member string) (recoverypb.CheckServiceClient, error) {
	c, err := g.conn(member)
	if err != nil {
		return nil, err
	}
	return recoverypb.NewCheckServiceClient(c), nil
}

func (g *GRPCCaller) MemberChecks(ctx context.Context, member string) ([]CheckOutcome, error) {
	cs, err := g.checks(member)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, verifyCallLimit)
	defer cancel()
	out, err := cs.Check(ctx, &recoverypb.CheckRequest{Recovery: g.Recovery})
	if err != nil {
		return nil, err
	}
	results := []CheckOutcome{}
	for _, r := range out.GetResults() {
		o := CheckOutcome{Check: r.GetCheck(), Basis: r.GetBasis(), Outcome: r.GetOutcome(), Disposal: r.GetDisposal(),
			Differences: []string{}}
		for _, d := range r.GetDifferences() {
			o.Differences = append(o.Differences, d.GetObjectType()+" "+d.GetObjectId()+": "+d.GetDetail())
		}
		results = append(results, o)
	}
	return results, nil
}

// presentBatch is how many objects one Present call asks about.
const presentBatch = 500

func (g *GRPCCaller) Present(ctx context.Context, owner string, objs []Asked) (map[string][2]string, error) {
	cs, err := g.checks(owner)
	if err != nil {
		return nil, err
	}
	out := map[string][2]string{}
	for from := 0; from < len(objs); from += presentBatch {
		req := &recoverypb.PresentRequest{Recovery: g.Recovery}
		for _, a := range objs[from:min(from+presentBatch, len(objs))] {
			req.Objects = append(req.Objects, &recoverypb.ObjectRef{Type: a.Type, Id: a.ID, Version: a.Version})
		}
		cctx, cancel := context.WithTimeout(ctx, verifyCallLimit)
		got, err := cs.Present(cctx, req)
		cancel()
		if err != nil {
			return nil, err
		}
		for _, a := range got.GetAnswers() {
			out[a.GetObject().GetId()+"\x00"+a.GetObject().GetType()] = [2]string{a.GetAnswer(), a.GetVersion()}
		}
	}
	return out, nil
}

func (g *GRPCCaller) Numbers(ctx context.Context, owner string) ([]Series, error) {
	cs, err := g.checks(owner)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, verifyCallLimit)
	defer cancel()
	got, err := cs.Numbers(ctx, &recoverypb.NumbersRequest{Recovery: g.Recovery})
	if err != nil {
		return nil, err
	}
	var out []Series
	for _, s := range got.GetSeries() {
		out = append(out, Series{Owner: owner, LegalEntity: s.GetLegalEntity(), Code: s.GetCode(), Period: s.GetPeriod(), Max: s.GetMaxUsed()})
	}
	return out, nil
}

func (g *GRPCCaller) Advance(ctx context.Context, owner string, series []Series) error {
	cs, err := g.checks(owner)
	if err != nil {
		return err
	}
	req := &recoverypb.AdvanceNumbersRequest{Recovery: g.Recovery}
	for _, s := range series {
		req.Series = append(req.Series, &recoverypb.Series{LegalEntity: s.LegalEntity, Code: s.Code, Period: s.Period, MaxUsed: s.Max})
	}
	ctx, cancel := context.WithTimeout(ctx, verifyCallLimit)
	defer cancel()
	_, err = cs.AdvanceNumbers(ctx, req)
	return err
}

// ListAudit reads the audit store's records that occurred at or after since (RecordService.List, the tool a reader).
func (g *GRPCCaller) ListAudit(ctx context.Context, since time.Time, categories []string) ([]AuditRecord, error) {
	c, err := g.conn(AuditID)
	if err != nil {
		return nil, err
	}
	client := auditv3.NewRecordServiceClient(c)
	var out []AuditRecord
	after := ""
	for {
		cctx, cancel := context.WithTimeout(ctx, sourcesCallLimit)
		page, err := client.List(cctx, &auditv3.RecordServiceListRequest{Since: since.UTC().Format(time.RFC3339Nano),
			Categories: categories, After: after, Limit: 500})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("the audit store: %w", err)
		}
		for _, r := range page.GetRecords() {
			a, err := ParseAudit(r.GetSource(), r.GetSourceEpoch(), r.GetSourceSeq(), r.GetRecord())
			if err != nil {
				return nil, fmt.Errorf("audit record at %s: %w", r.GetPosition(), err)
			}
			out = append(out, a)
			after = r.GetPosition()
		}
		if !page.GetMore() || len(page.GetRecords()) == 0 {
			return out, nil
		}
	}
}

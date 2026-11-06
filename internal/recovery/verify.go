package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/brickKit/be-sdk-go/v3/runprofile"
)

// Steps 7-10 (assembly protocol 15; be-protocol chapters 13 and 14): the deployment restarts verifying, the adapters
// reconcile (7), every member with a database answers its checks (8), the loss is listed from the audit store (9);
// with the checks passed (or their differences accepted by an approver) it opens for reading, then, with the
// continuous number series raised past what the audit names, for writing (10). Each step's list goes into the run
// profile; the calls run in the deployment's network like step 6's.

// VerifyOptions are the flags of steps 7-10.
type VerifyOptions struct {
	Binary   string        // the tool's own binary, mounted into the calls' container
	Window   time.Duration // step 7: how far before T the external records are listed (clock skew + posting delay)
	FailedAt string        // the failure moment (RFC 3339); empty: the call's time
	Approver string        // steps 10: who decided
	Reason   string        // open-read: why the differences are accepted
}

func (t *Tool) verifyInput(st Status, kind string, o VerifyOptions) VerifyInput {
	return VerifyInput{Kind: kind, Recovery: st.Recovery, T: st.T.UTC().Format(time.RFC3339Nano), FailedAt: o.FailedAt,
		Window: int64(o.Window.Seconds()), Addresses: t.Project.SystemAddresses(), Members: t.Project.Checked("verifying"),
		Senders: t.Project.Senders()}
}

// Reconcile is step 7: every member restarts verifying (adapters query only, P13.10), then each adapter reconciles.
func (t *Tool) Reconcile(ctx context.Context, o VerifyOptions) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := Allowed(st, StepReconcile); err != nil {
		return err
	}
	if err := t.phase(ctx, "verifying"); err != nil {
		return err
	}
	var rep VerifyReport
	if err := t.runCalls(ctx, o.Binary, "verify-calls", t.verifyInput(st, CallsReconcile, o), &rep); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, cs := range rep.Conclusions {
		for _, c := range cs {
			counts[c.Conclusion]++
		}
	}
	t.say("reconciled %d adapters: %v; without the method: %v", len(rep.Conclusions), counts, rep.Unimplemented)
	return t.record(ctx, st, Step{Step: StepReconcile, Detail: map[string]any{"report": rep, "conclusions": counts,
		"window_before_seconds": int64(o.Window.Seconds()), "failed_at": o.FailedAt}})
}

// Check is step 8: every member's checks, the list into the run profile.
func (t *Tool) Check(ctx context.Context, o VerifyOptions) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := Allowed(st, StepCheck); err != nil {
		return err
	}
	var rep VerifyReport
	if err := t.runCalls(ctx, o.Binary, "verify-calls", t.verifyInput(st, CallsCheck, o), &rep); err != nil {
		return err
	}
	diffs := 0
	for m, cs := range rep.Checks {
		for _, c := range cs {
			if c.Outcome != "pass" {
				diffs++
				t.say("%s %s: %s (%d differences) %s", m, c.Check, c.Outcome, len(c.Differences), c.Disposal)
			}
		}
	}
	t.say("checked %d members: pass %v; not passed %d; without the method %v; failed %v", len(rep.Checks), rep.Pass, diffs,
		rep.Unimplemented, rep.Failed)
	return t.record(ctx, st, Step{Step: StepCheck, Detail: map[string]any{"report": rep, "pass": rep.Pass}})
}

// Loss is step 9: what the audit store names after T that the recovery did not keep, the audit loss, and the
// references step 7 found executed outside (or possibly) that must not be entered again.
func (t *Tool) Loss(ctx context.Context, o VerifyOptions) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := Allowed(st, StepLoss); err != nil {
		return err
	}
	if o.FailedAt == "" {
		o.FailedAt = t.lastString(ctx, st, StepReconcile, "failed_at")
	}
	var rep VerifyReport
	if err := t.runCalls(ctx, o.Binary, "verify-calls", t.verifyInput(st, CallsLoss, o), &rep); err != nil {
		return err
	}
	noReentry, err := t.noReentry(ctx, st)
	if err != nil {
		return err
	}
	lost := map[string]int{}
	for _, l := range rep.Lost {
		lost[l.Verdict]++
	}
	t.say("the loss: %d objects asked, %v; %d records name no object; audit loss of %d sources; not to be entered again: %d; "+
		"double failures (tightenings without evidence, for review): %d", rep.Asked, lost, rep.Unnamed, len(rep.AuditLoss),
		len(noReentry), len(rep.EvidenceLoss))
	return t.record(ctx, st, Step{Step: StepLoss, Detail: map[string]any{"report": rep, "verdicts": lost,
		"no_reentry": noReentry}})
}

// noReentry are step 7's references executed outside without a local record, or possibly executed (protocol 15, step
// 9: entering them again would execute them twice).
func (t *Tool) noReentry(ctx context.Context, st Status) ([]string, error) {
	detail, err := t.lastDetail(ctx, st, StepReconcile)
	if err != nil || detail == nil {
		return []string{}, err
	}
	raw, err := json.Marshal(detail["report"])
	if err != nil {
		return nil, err
	}
	var rep VerifyReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, err
	}
	out := []string{}
	for adapter, cs := range rep.Conclusions {
		for _, c := range cs {
			if c.Conclusion == "external_only" || c.Conclusion == "possibly_executed" {
				out = append(out, adapter+" "+c.Reference+" ("+c.Conclusion+")")
			}
		}
	}
	return out, nil
}

// lastDetail is the detail of the latest record of a step in this recovery (nil: none).
func (t *Tool) lastDetail(ctx context.Context, st Status, step string) (map[string]any, error) {
	recs, _, err := t.Profile.List(ctx, runprofile.KindRecoveryStep)
	if err != nil {
		return nil, err
	}
	var last *Step
	for _, r := range recs {
		s, err := Decode(r.Body)
		if err != nil || s.Step != step || s.Recovery != st.Recovery {
			continue
		}
		if last == nil || s.At >= last.At {
			last = &s
		}
	}
	if last == nil {
		return nil, nil
	}
	return last.Detail, nil
}

func (t *Tool) lastString(ctx context.Context, st Status, step, key string) string {
	d, err := t.lastDetail(ctx, st, step)
	if err != nil || d == nil {
		return ""
	}
	s, _ := d[key].(string)
	return s
}

// OpenRead is step 10's first half: the checks passed, or an approver accepts their differences with a reason; the
// loss is listed; every member restarts read-only. Two guards of protocol 15's state table are the approver's to
// confirm in this version, not checked by the tool: every verifier has refreshed the key set, and an authorization
// admin can log in.
func (t *Tool) OpenRead(ctx context.Context, o VerifyOptions) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := Allowed(st, StepOpenRead); err != nil {
		return err
	}
	if o.Approver == "" {
		return fmt.Errorf("--approver: who opens the deployment for reading")
	}
	check, err := t.lastDetail(ctx, st, StepCheck)
	if err != nil {
		return err
	}
	pass, _ := check["pass"].(bool)
	if !pass && strings.TrimSpace(o.Reason) == "" {
		return fmt.Errorf("the checks did not all pass (see the check step's list): --reason says why the differences are accepted")
	}
	if err := t.phase(ctx, "read_only"); err != nil {
		return err
	}
	t.say("open for reading; the checks passed: %v", pass)
	return t.record(ctx, st, Step{Step: StepOpenRead, Approver: o.Approver, Reason: o.Reason, Detail: map[string]any{
		"checks_passed": pass, "confirmed_by_approver": []string{"every verifier refreshed the key set",
			"an authorization admin can log in"}}})
}

// Open is step 10's second half: the continuous series raised past the numbers the audit names after T (P14.5),
// then every member restarts running; the recovery is over.
func (t *Tool) Open(ctx context.Context, o VerifyOptions) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := Allowed(st, StepOpen); err != nil {
		return err
	}
	if o.Approver == "" {
		return fmt.Errorf("--approver: who opens the deployment for writing")
	}
	in := t.verifyInput(st, CallsNumbers, o)
	in.Members, in.Raise = t.Project.Checked("read_only"), true
	var rep VerifyReport
	if err := t.runCalls(ctx, o.Binary, "verify-calls", in, &rep); err != nil {
		return err
	}
	t.say("number series: %d, raised %d (%v); without the method %v", len(rep.Series), len(rep.Raised), rep.Raised, rep.Unimplemented)
	if err := t.phase(ctx, ""); err != nil {
		return err
	}
	return t.record(ctx, st, Step{Step: StepOpen, Approver: o.Approver, Detail: map[string]any{"report": rep}})
}

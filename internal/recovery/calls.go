package recovery

import (
	"context"
	"fmt"
	"time"

	"github.com/brickKit/be-sdk-go/v3/evidence"
	"github.com/brickKit/be-sdk-go/v3/identity"
	authzv3 "github.com/brickKit/contract-infra-authz/v3/gen/go/be/authz/v3"
	auditv3 "github.com/brickKit/infra-audit/contract/v3/gen/go/be/audit/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// The calls of step 6 over the deployment's system surfaces, with the tool's own service identity (ops/recover):
// plain gRPC with mutual TLS, each call with its own deadline. The tool is no component: it has no call policy of
// its own to follow, and it needs the owners' status codes as they are (a failure to reach one stops the step).

// Per-call deadlines: one record's replay, a Sources answer, a deferred replay, the invariant check (every record).
const (
	replayCallLimit  = 30 * time.Second
	sourcesCallLimit = 10 * time.Second
	checkCallLimit   = 5 * time.Minute
)

// GRPCCaller is the Caller over gRPC. Addresses are host:port of each component's system surface; Recovery is the
// recovery's identity (the run profile's), carried on every replay.
type GRPCCaller struct {
	TLS       *identity.ServiceTLS
	Addresses map[string]string
	Recovery  string
	conns     map[string]*grpc.ClientConn
}

func (g *GRPCCaller) conn(component string) (*grpc.ClientConn, error) {
	if c := g.conns[component]; c != nil {
		return c, nil
	}
	addr := g.Addresses[component]
	if addr == "" {
		return nil, fmt.Errorf("no system address of %s", component)
	}
	c, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(g.TLS.ClientConfig(component))))
	if err != nil {
		return nil, err
	}
	if g.conns == nil {
		g.conns = map[string]*grpc.ClientConn{}
	}
	g.conns[component] = c
	return c, nil
}

// Close closes the connections.
func (g *GRPCCaller) Close() {
	for _, c := range g.conns {
		_ = c.Close()
	}
}

// answered sorts an owner's error: out of reach (the step stops), no ReplayService, or an answer that is a failure.
func answered(err error) (outcome, detail string, stop error) {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled, codes.PermissionDenied, codes.Unauthenticated:
		return "", "", err
	case codes.Unimplemented:
		return OutcomeUnimplemented, err.Error(), nil
	}
	return OutcomeFailed, err.Error(), nil
}

func (g *GRPCCaller) Replay(ctx context.Context, owner string, record []byte) (string, string, error) {
	c, err := g.conn(owner)
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(ctx, replayCallLimit)
	defer cancel()
	outcome, detail, err := evidence.ReplayClient{Conn: c}.Replay(ctx, record, g.Recovery)
	if err != nil {
		return answered(err)
	}
	return outcome, detail, nil
}

func (g *GRPCCaller) recovery() (authzv3.RecoveryServiceClient, error) {
	c, err := g.conn(AuthzID)
	if err != nil {
		return nil, err
	}
	return authzv3.NewRecoveryServiceClient(c), nil
}

func (g *GRPCCaller) Sources(ctx context.Context, since string) (string, bool, error) {
	rc, err := g.recovery()
	if err != nil {
		return "", false, err
	}
	ctx, cancel := context.WithTimeout(ctx, sourcesCallLimit)
	defer cancel()
	out, err := rc.Sources(ctx, &authzv3.SourcesRequest{Since: since})
	if err != nil {
		return "", false, err
	}
	return out.GetAsOf(), out.GetAllApplied(), nil
}

func (g *GRPCCaller) ReplayDeferred(ctx context.Context, record []byte) (string, string, error) {
	rc, err := g.recovery()
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(ctx, replayCallLimit)
	defer cancel()
	out, err := rc.ReplayDeferred(ctx, &authzv3.ReplayDeferredRequest{Record: record, Recovery: g.Recovery})
	if err != nil {
		return answered(err)
	}
	return out.GetOutcome(), out.GetDetail(), nil
}

func (g *GRPCCaller) Check(ctx context.Context, subjects []string) (CheckResult, error) {
	rc, err := g.recovery()
	if err != nil {
		return CheckResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, checkCallLimit)
	defer cancel()
	out, err := rc.Check(ctx, &authzv3.CheckRequest{Recovery: g.Recovery, ReviewSubjects: subjects})
	if err != nil {
		return CheckResult{}, err
	}
	return CheckResult{AdminState: out.GetAdminState(), Ended: out.GetEnded(), Held: out.GetHeld(), Review: out.GetReview(),
		Closed: out.GetClosed(), AdminsNotClosed: out.GetAdminsNotClosed(), Unprovable: out.GetUnprovable()}, nil
}

// ListEvidence reads every record of the evidence store (infra/audit's EvidenceService.List, the tool a reader),
// in the store's order.
func (g *GRPCCaller) ListEvidence(ctx context.Context) ([][]byte, error) {
	c, err := g.conn(AuditID)
	if err != nil {
		return nil, err
	}
	client := auditv3.NewEvidenceServiceClient(c)
	var out [][]byte
	var after int64
	for {
		cctx, cancel := context.WithTimeout(ctx, sourcesCallLimit)
		page, err := client.List(cctx, &auditv3.ListRequest{AfterSeq: after, Limit: 500})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("the evidence store: %w", err)
		}
		for _, r := range page.GetRecords() {
			out = append(out, r.GetEvidence().GetRecord())
			after = r.GetSeq()
		}
		if !page.GetMore() || len(page.GetRecords()) == 0 {
			return out, nil
		}
	}
}

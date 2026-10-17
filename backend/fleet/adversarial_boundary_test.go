package fleet

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/proposal"
)

func TestAdversarialExecutionGateRequiresApprovalAndTrustedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   proposal.State
		ctx     context.Context
		subject string
		expired bool
		want    error
	}{
		{"pending", proposal.Pending, trustedContext(), testOperator.Subject, false, proposal.ErrInvalidTransition},
		{"rejected", proposal.Rejected, trustedContext(), testOperator.Subject, false, proposal.ErrInvalidTransition},
		{"expired approval", proposal.Approved, trustedContext(), testOperator.Subject, true, proposal.ErrExpired},
		{"forged approval without trusted context", proposal.Approved, context.Background(), testOperator.Subject, false, ErrUnauthorized},
		{"impersonated operator", proposal.Approved, trustedContext(), "model-authored-operator", false, ErrUnauthorized},
		{"approved control checks eligibility only", proposal.Approved, trustedContext(), testOperator.Subject, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := unitService(t)
			p := proposal.Proposal{ID: "adversarial-unit", Action: proposal.SetCharging, Request: chargingRequest,
				State: tc.state, CreatedAt: testTime, ExpiresAt: testTime.Add(time.Minute)}
			if tc.expired {
				p.ExpiresAt = testTime
			}
			before := s.Snapshot()
			err := s.executionGate(tc.ctx, p, proposal.OperatorIdentity{Subject: tc.subject})
			if !errors.Is(err, tc.want) {
				t.Fatalf("gate=%v want=%v", err, tc.want)
			}
			if !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatal("execution eligibility check mutated simulator")
			}
		})
	}
}

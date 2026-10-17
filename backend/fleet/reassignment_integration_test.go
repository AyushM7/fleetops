//go:build fleetops_test

package fleet

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

var reassignmentRequest = safety.ReassignUnstartedJobRequest{JobID: "J1", FromRobotID: "R1", ToRobotID: "R3"}

func TestIntegrationReassignmentFailClosed(t *testing.T) {
	for _, failure := range []string{"submit commit", "execute commit", "commit acknowledgement"} {
		t.Run(failure, func(t *testing.T) {
			pool := integrationPool(t)
			simulator, err := sim.NewReassignmentTestFixture()
			if err != nil {
				t.Fatal(err)
			}
			s, err := newService(simulator, simulator.Tick, pool, func() time.Time { return testTime }, authorized)
			if err != nil {
				t.Fatal(err)
			}
			before := s.Snapshot()
			if failure == "submit commit" {
				_, err = pool.Exec(context.Background(), `CREATE FUNCTION fail_submit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test commit failure'; END $$;
CREATE CONSTRAINT TRIGGER fail_submit AFTER INSERT ON fleet_proposals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_submit();`)
				if err != nil {
					t.Fatal(err)
				}
			}
			r, err := s.SubmitReassignment(context.Background(), reassignmentRequest, testTime.Add(time.Minute))
			if failure == "submit commit" {
				if !errors.Is(err, ErrPersistence) || r.AuditCommitted || !s.Poisoned() || !reflect.DeepEqual(before, s.Snapshot()) {
					t.Fatal("submission not fail closed", r, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if failure == "execute commit" {
				_, err = pool.Exec(context.Background(), `CREATE FUNCTION fail_execute() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.attempt='execute' THEN RAISE EXCEPTION 'test commit failure'; END IF; RETURN NEW; END $$;
CREATE CONSTRAINT TRIGGER fail_execute AFTER INSERT ON fleet_audit DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_execute();`)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				s.transact = func(ctx context.Context, body func(*db.Tx) error) error {
					if err := pool.Transaction(ctx, body); err != nil {
						return err
					}
					return db.ErrQuery
				}
			}
			r, err = s.Decide(trustedContext(), r.Proposal.ID, proposal.Approve, testOperator)
			if !errors.Is(err, ErrPersistence) || !s.Poisoned() || r.AuditCommitted || !r.ExecutionAttempted || r.Execution != Unknown {
				t.Fatal("uncertain execution was acknowledged", r, err)
			}
			if s.Snapshot().Jobs[0].AssignedRobot != "R3" {
				t.Fatal("test did not cross irreversible transfer boundary")
			}
			after := s.Snapshot()
			if err := s.Tick(); !errors.Is(err, ErrPoisoned) {
				t.Fatal("poisoned runtime ticked")
			}
			if !reflect.DeepEqual(after, s.Snapshot()) {
				t.Fatal("poisoned runtime changed")
			}
		})
	}
}

func TestIntegrationReassignmentStaleAndExecutionBoundary(t *testing.T) {
	for _, failure := range []string{"recipient battery", "expiry", "authorization"} {
		t.Run(failure, func(t *testing.T) {
			pool := integrationPool(t)
			simulator, err := sim.NewReassignmentTestFixture()
			if err != nil {
				t.Fatal(err)
			}
			now := testTime
			s, err := newService(simulator, simulator.Tick, pool, func() time.Time { return now }, authorized)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.SubmitReassignment(context.Background(), reassignmentRequest, now.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if failure == "recipient battery" {
				if err := simulator.InjectLowBattery("R3"); err != nil {
					t.Fatal(err)
				}
			}
			checks := 0
			s.authorize = func(ctx context.Context, op proposal.OperatorIdentity) bool {
				checks++
				if checks == 2 {
					if failure == "expiry" {
						now = now.Add(time.Minute)
					}
					if failure == "authorization" {
						return false
					}
				}
				return authorized(ctx, op)
			}
			before := s.Snapshot()
			r, err = s.Decide(trustedContext(), r.Proposal.ID, proposal.Approve, testOperator)
			if err == nil || !r.AuditCommitted || r.ExecutionAttempted || !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatal("stale or denied execution changed state", r, err)
			}
		})
	}
}

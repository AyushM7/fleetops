package proposal_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

var (
	start    = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	request  = safety.SetChargingRequest{RobotID: "R1", StationID: "S1"}
	operator = proposal.OperatorIdentity{Subject: "operator-7"}
)

type fixtureReader struct {
	sim.Reader
	state sim.State
	reads int
}

func (r *fixtureReader) Snapshot() sim.State {
	r.reads++
	return r.state
}

func fixture(t *testing.T) (*proposal.Lifecycle, *time.Time, *fixtureReader) {
	t.Helper()
	now := start
	reader := &fixtureReader{state: sim.State{
		Robots:   []sim.RobotState{{ID: "R1", Battery: 30, Status: sim.RobotIdle}},
		Stations: []sim.StationState{{ID: "S1"}},
	}}
	lifecycle, err := proposal.New(reader, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return lifecycle, &now, reader
}

func create(t *testing.T, l *proposal.Lifecycle) proposal.Result {
	t.Helper()
	result, err := l.Create("P1", request, start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertEvent(t *testing.T, result proposal.Result, err, want error, attempt proposal.Attempt, before proposal.State, at time.Time) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	event := result.Event
	if event.Attempt != attempt || event.Before != before || !event.At.Equal(at) || event.Accepted != (want == nil) || !reflect.DeepEqual(event.Proposal, result.Proposal) {
		t.Fatalf("unexpected event: %+v", event)
	}
	if want == nil && event.Reason != "" || want != nil && event.Reason != want.Error() {
		t.Fatalf("reason = %q, want error %v", event.Reason, want)
	}
}

func TestCreation(t *testing.T) {
	l, _, reader := fixture(t)
	r := create(t, l)
	want := proposal.Proposal{
		ID: "P1", Action: proposal.SetCharging, Request: request,
		CreatedAt: start, ExpiresAt: start.Add(time.Minute), State: proposal.Pending,
	}
	if r.Proposal != want || reader.reads != 1 {
		t.Fatalf("proposal = %+v, reads = %d", r.Proposal, reader.reads)
	}
	assertEvent(t, r, nil, nil, proposal.AttemptCreate, "", start)
	if r.Event.Operator != (proposal.OperatorIdentity{}) {
		t.Fatal("proposal creation must not assert a human identity")
	}
	second, err := l.Create("P2", request, start.Add(time.Minute))
	if err != nil || second.Proposal.ID == r.Proposal.ID {
		t.Fatalf("distinct proposal creation: %+v, %v", second, err)
	}
}

func TestInvalidCreation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		id     string
		expiry time.Time
		want   error
	}{
		{name: "empty ID", expiry: start.Add(time.Minute), want: proposal.ErrInvalidID},
		{name: "whitespace ID", id: " P1", expiry: start.Add(time.Minute), want: proposal.ErrInvalidID},
		{name: "control ID", id: "P\x001", expiry: start.Add(time.Minute), want: proposal.ErrInvalidID},
		{name: "zero expiry", id: "P1", want: proposal.ErrInvalidExpiration},
		{name: "expiry equal creation", id: "P1", expiry: start, want: proposal.ErrInvalidExpiration},
		{name: "expiry before creation", id: "P1", expiry: start.Add(-time.Nanosecond), want: proposal.ErrInvalidExpiration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, _, reader := fixture(t)
			r, err := l.Create(tc.id, request, tc.expiry)
			assertEvent(t, r, err, tc.want, proposal.AttemptCreate, "", start)
			if r.Proposal.State != "" || reader.reads != 0 {
				t.Fatal("invalid creation admitted a proposal or read the simulator")
			}
		})
	}
}

func TestCreationPreflightAndDuplicateIdentity(t *testing.T) {
	l, _, reader := fixture(t)
	reader.state.Robots[0].Battery = 29
	r, err := l.Create("P1", request, start.Add(time.Minute))
	assertEvent(t, r, err, safety.ErrBatteryBelowMinimum, proposal.AttemptCreate, "", start)
	reader.state.Robots[0].Battery = 30
	canonical := create(t, l).Proposal
	changed := safety.SetChargingRequest{RobotID: "other", StationID: "other"}
	r, err = l.Create("P1", changed, start.Add(2*time.Minute))
	assertEvent(t, r, err, proposal.ErrDuplicateID, proposal.AttemptCreate, "", start)
	if r.Event.Proposal.Request != changed {
		t.Fatal("duplicate creation event lost the attempted parameters")
	}
	r, err = l.Decide("P1", proposal.Reject, operator)
	if err != nil || r.Proposal.Request != canonical.Request || r.Proposal.ExpiresAt != canonical.ExpiresAt {
		t.Fatalf("duplicate creation replaced canonical parameters: %+v, %v", r, err)
	}
}

func TestHumanDecisions(t *testing.T) {
	for _, decision := range []proposal.Decision{proposal.Approve, proposal.Reject} {
		t.Run(string(decision), func(t *testing.T) {
			l, now, reader := fixture(t)
			create(t, l)
			*now = start.Add(time.Second)
			r, err := l.Decide("P1", decision, operator)
			assertEvent(t, r, err, nil, proposal.Attempt(decision), proposal.Pending, *now)
			want := proposal.Approved
			reads := 2
			if decision == proposal.Reject {
				want, reads = proposal.Rejected, 1
			}
			if r.Proposal.State != want || r.Proposal.Operator != operator || r.Event.Operator != operator || r.Proposal.DecidedAt != *now || reader.reads != reads {
				t.Fatalf("decision = %+v, reads = %d", r, reader.reads)
			}
		})
	}
}

func TestDuplicateAndConflictingDecisions(t *testing.T) {
	for _, first := range []proposal.Decision{proposal.Approve, proposal.Reject} {
		for _, next := range []proposal.Decision{proposal.Approve, proposal.Reject} {
			t.Run(string(first)+"/"+string(next), func(t *testing.T) {
				l, now, reader := fixture(t)
				create(t, l)
				accepted, err := l.Decide("P1", first, operator)
				if err != nil {
					t.Fatal(err)
				}
				reads := reader.reads
				*now = start.Add(time.Second)
				r, err := l.Decide("P1", next, proposal.OperatorIdentity{Subject: "other-operator"})
				assertEvent(t, r, err, proposal.ErrInvalidTransition, proposal.Attempt(next), accepted.Proposal.State, *now)
				if r.Proposal != accepted.Proposal || reader.reads != reads || r.Event.Operator.Subject != "other-operator" {
					t.Fatal("denied attempt changed the original decision or lost attempted identity")
				}
			})
		}
	}
}

func TestInvalidOperatorAndDecision(t *testing.T) {
	for _, subject := range []string{"", " ", " operator", "operator ", "operator\n7", "operator\x007"} {
		for _, decision := range []proposal.Decision{proposal.Approve, proposal.Reject} {
			t.Run(string(decision)+"/"+subject, func(t *testing.T) {
				l, _, reader := fixture(t)
				pending := create(t, l).Proposal
				identity := proposal.OperatorIdentity{Subject: subject}
				r, err := l.Decide("P1", decision, identity)
				assertEvent(t, r, err, proposal.ErrInvalidOperator, proposal.Attempt(decision), proposal.Pending, start)
				if r.Proposal != pending || r.Event.Operator != identity || reader.reads != 1 {
					t.Fatal("invalid identity changed state or lost attempted identity")
				}
			})
		}
	}
	l, _, _ := fixture(t)
	create(t, l)
	r, err := l.Decide("P1", "execute", operator)
	assertEvent(t, r, err, proposal.ErrInvalidDecision, "execute", proposal.Pending, start)
	r, err = l.Decide("missing", proposal.Approve, operator)
	assertEvent(t, r, err, proposal.ErrUnknownProposal, "approve", "", start)
	if r.Proposal.ID != "missing" {
		t.Fatal("unknown proposal event lost attempted ID")
	}
}

func TestApprovalReusesCurrentPreflight(t *testing.T) {
	l, now, reader := fixture(t)
	pending := create(t, l).Proposal
	*now = start.Add(time.Second)
	reader.state.Robots[0].Status = sim.RobotOffline
	r, err := l.Decide("P1", proposal.Approve, operator)
	assertEvent(t, r, err, sim.ErrRobotUnavailable, "approve", proposal.Pending, *now)
	if r.Proposal != pending {
		t.Fatal("failed preflight changed proposal")
	}
	// A failed approval attempt does not prevent an explicit human rejection.
	r, err = l.Decide("P1", proposal.Reject, operator)
	assertEvent(t, r, err, nil, "reject", proposal.Pending, *now)
}

func TestExpirationBoundaries(t *testing.T) {
	for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		for _, decision := range []proposal.Decision{proposal.Approve, proposal.Reject} {
			t.Run(string(decision)+"/"+offset.String(), func(t *testing.T) {
				l, now, reader := fixture(t)
				create(t, l)
				*now = start.Add(time.Minute + offset)
				want := proposal.ErrExpired
				if offset < 0 {
					want = nil
				}
				r, err := l.Decide("P1", decision, operator)
				assertEvent(t, r, err, want, proposal.Attempt(decision), proposal.Pending, *now)
				if offset >= 0 && (r.Proposal.State != proposal.Expired || reader.reads != 1 || !r.Proposal.DecidedAt.IsZero()) {
					t.Fatal("expired proposal read simulator or recorded a human decision")
				}
			})
		}
	}
}

func TestExplicitExpirationAndFinalStates(t *testing.T) {
	for _, decision := range []proposal.Decision{"", proposal.Approve, proposal.Reject} {
		t.Run(string(decision), func(t *testing.T) {
			l, now, reader := fixture(t)
			initial := create(t, l).Proposal
			if decision != "" {
				r, err := l.Decide("P1", decision, operator)
				if err != nil {
					t.Fatal(err)
				}
				initial = r.Proposal
			}
			r, err := l.Expire("P1")
			want := proposal.ErrNotExpired
			if decision == proposal.Reject {
				want = proposal.ErrInvalidTransition
			}
			assertEvent(t, r, err, want, proposal.AttemptExpire, initial.State, start)
			*now = start.Add(time.Minute)
			reads := reader.reads
			r, err = l.Expire("P1")
			want = nil
			if decision == proposal.Reject {
				want = proposal.ErrInvalidTransition
			}
			assertEvent(t, r, err, want, proposal.AttemptExpire, initial.State, *now)
			if r.Proposal.Operator != initial.Operator || r.Proposal.DecidedAt != initial.DecidedAt || reader.reads != reads {
				t.Fatal("expiration changed the human decision or read the simulator")
			}
			if decision == proposal.Reject {
				if r.Proposal != initial {
					t.Fatal("rejected proposal changed")
				}
				return
			}
			if r.Proposal.State != proposal.Expired {
				t.Fatal("proposal did not expire")
			}
			r, err = l.Expire("P1")
			assertEvent(t, r, err, proposal.ErrExpired, proposal.AttemptExpire, proposal.Expired, *now)
			*now = start.Add(time.Second)
			r, err = l.Decide("P1", proposal.Approve, operator)
			assertEvent(t, r, err, proposal.ErrExpired, "approve", proposal.Expired, *now)
		})
	}
	l, _, _ := fixture(t)
	r, err := l.Expire("missing")
	assertEvent(t, r, err, proposal.ErrUnknownProposal, proposal.AttemptExpire, "", start)
}

func TestImmutableParametersAndEvents(t *testing.T) {
	l, _, _ := fixture(t)
	input := request
	r, err := l.Create("P1", input, start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	canonical := r.Proposal
	input.RobotID = "tampered"
	r.Proposal.ID, r.Proposal.Request.RobotID = "changed", "changed"
	r.Proposal.State, r.Proposal.ExpiresAt = proposal.Approved, start.Add(time.Hour)
	r.Event.Proposal.Request.StationID = "changed"
	r, err = l.Decide("P1", proposal.Approve, operator)
	if err != nil || r.Proposal.Request != canonical.Request || r.Proposal.ExpiresAt != canonical.ExpiresAt || r.Proposal.ID != canonical.ID {
		t.Fatalf("canonical parameters changed: %+v, %v", r, err)
	}
	r.Proposal.Operator.Subject = "tampered"
	r.Event.Proposal.Operator.Subject = "tampered"
	next, err := l.Decide("P1", proposal.Reject, operator)
	if !errors.Is(err, proposal.ErrInvalidTransition) || next.Proposal.Operator != operator {
		t.Fatal("returned decision view mutated canonical operator")
	}
}

func TestClockAndDependencies(t *testing.T) {
	if _, err := proposal.New(nil, func() time.Time { return start }); !errors.Is(err, proposal.ErrInvalidDependencies) {
		t.Fatal(err)
	}
	if _, err := proposal.New(&fixtureReader{}, nil); !errors.Is(err, proposal.ErrInvalidDependencies) {
		t.Fatal(err)
	}
	l, now, _ := fixture(t)
	*now = time.Time{}
	r, err := l.Create("P1", request, start.Add(time.Minute))
	assertEvent(t, r, err, proposal.ErrInvalidClock, proposal.AttemptCreate, "", *now)
	*now = start
	create(t, l)
	*now = start.Add(-time.Nanosecond)
	r, err = l.Decide("P1", proposal.Approve, operator)
	assertEvent(t, r, err, proposal.ErrInvalidClock, "approve", proposal.Pending, *now)
}

func TestDeterministicLifecycle(t *testing.T) {
	run := func() []proposal.Result {
		l, now, _ := fixture(t)
		var results []proposal.Result
		results = append(results, create(t, l))
		*now = start.Add(time.Second).In(time.FixedZone("offset", 3600))
		r, _ := l.Decide("P1", proposal.Approve, operator)
		results = append(results, r)
		r, _ = l.Decide("P1", proposal.Reject, operator)
		results = append(results, r)
		*now = start.Add(time.Minute)
		r, _ = l.Expire("P1")
		return append(results, r)
	}
	want := run()
	for range 20 {
		if got := run(); !reflect.DeepEqual(got, want) {
			t.Fatalf("lifecycle replay differs: %+v", got)
		}
	}
	if want[1].Event.At.Location() != time.UTC {
		t.Fatal("event timestamps must be UTC")
	}
}

func TestNoSimulatorMutationOrExecution(t *testing.T) {
	cfg := sim.DefaultConfig()
	cfg.RobotStarts[0] = cfg.Stations[0]
	subject, err := sim.New(cfg, 42)
	if err != nil {
		t.Fatal(err)
	}
	control, err := sim.New(cfg, 42)
	if err != nil {
		t.Fatal(err)
	}
	now := start
	// Only Reader methods cross the domain boundary.
	l, err := proposal.New(struct{ sim.Reader }{subject}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	before := subject.Snapshot()
	create(t, l)
	if _, err := l.Decide("P1", proposal.Approve, operator); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Create("P2", request, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Decide("P2", proposal.Reject, operator); err != nil {
		t.Fatal(err)
	}
	now = start.Add(time.Minute)
	if _, err := l.Expire("P1"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Decide("P1", proposal.Approve, operator); !errors.Is(err, proposal.ErrExpired) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(subject.Snapshot(), before) {
		t.Fatal("lifecycle mutated simulator state")
	}
	for tick := 0; tick < 100; tick++ {
		subject.Tick()
		control.Tick()
		if !reflect.DeepEqual(subject.Snapshot(), control.Snapshot()) {
			t.Fatalf("lifecycle changed simulator replay at tick %d", tick+1)
		}
	}
}

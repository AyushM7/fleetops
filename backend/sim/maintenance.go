package sim

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
)

type MaintenancePhase string

const (
	MaintenanceAvailable   MaintenancePhase = "available"
	MaintenanceAssigned    MaintenancePhase = "assigned"
	MaintenanceTraveling   MaintenancePhase = "traveling"
	MaintenanceAtTarget    MaintenancePhase = "at_target"
	MaintenanceRepairing   MaintenancePhase = "repairing"
	MaintenanceVerifying   MaintenancePhase = "verifying"
	MaintenanceReturning   MaintenancePhase = "returning"
	MaintenanceUnavailable MaintenancePhase = "unavailable"
)

type MaintenanceOutcome string

const (
	MaintenanceCompleted  MaintenanceOutcome = "completed"
	MaintenanceFailed     MaintenanceOutcome = "failed"
	MaintenanceUnresolved MaintenanceOutcome = "unresolved"
	MaintenanceCancelled  MaintenanceOutcome = "cancelled"
)

type MaintenanceReason string

const (
	maintenanceBusy            MaintenanceReason = "maintenance_unavailable"
	maintenanceExternalPower   MaintenanceReason = "external_power_required"
	maintenanceAwaitingOffline MaintenanceReason = "awaiting_offline"
	maintenanceUnreachable     MaintenanceReason = "service_cell_unreachable"
	maintenanceInsufficient    MaintenanceReason = "insufficient_maintenance_energy"
	maintenanceTargetChanged   MaintenanceReason = "target_changed"
	maintenanceSiteLost        MaintenanceReason = "service_position_lost"
	maintenanceBlocked         MaintenanceReason = "travel_blocked"
	maintenanceRecoveryBlocked MaintenanceReason = "target_recovery_blocked"
	maintenanceSelfRepair      MaintenanceReason = "self_repair_unsupported"
	maintenanceAlreadyTried    MaintenanceReason = "attempt_already_recorded"
)

// MaintenanceRequest binds admission to a specific active fault occurrence.
// Deterministic and Operations dispatch share the preview and admission path.
type MaintenanceRequest struct {
	RobotID string `json:"robotId"`
	FaultID string `json:"faultId"`
}

type MaintenanceIssue struct {
	MaintenanceRequest
	Reason MaintenanceReason `json:"reason"`
}

type MaintenanceBreakdown struct {
	MaintenanceRequest
	Fault  FaultType         `json:"fault"`
	Tick   int               `json:"tick"`
	Status RobotStatus       `json:"status"`
	Reason MaintenanceReason `json:"reason,omitempty"`
}

type MaintenanceOption struct {
	MaintenanceRequest
	JobID           string `json:"jobId,omitempty"`
	ServiceCell     Point  `json:"serviceCell"`
	TravelSteps     int    `json:"travelSteps"`
	ExitSteps       int    `json:"exitSteps"`
	RepairTicks     int    `json:"repairTicks"`
	RequiredBattery int    `json:"requiredBattery"`
}

var ErrMaintenanceWindow = errors.New("maintenance dispatch window is closed")

type MaintenanceAttempt struct {
	MaintenanceRequest
	ID                  string             `json:"id"`
	JobID               string             `json:"jobId,omitempty"`
	Fault               FaultType          `json:"fault"`
	TargetCell          Point              `json:"targetCell"`
	ServiceCell         Point              `json:"serviceCell"`
	StartedTick         int                `json:"startedTick"`
	FinishedTick        int                `json:"finishedTick,omitempty"`
	RepairTicks         int                `json:"repairTicks"`
	RequiredRepairTicks int                `json:"requiredRepairTicks"`
	Outcome             MaintenanceOutcome `json:"outcome,omitempty"`
	Reason              MaintenanceReason  `json:"reason,omitempty"`
	AIDirected          bool               `json:"aiDirected,omitempty"`
}

type MaintenanceState struct {
	Phase   MaintenancePhase     `json:"phase"`
	Active  *MaintenanceAttempt  `json:"active,omitempty"`
	Issues  []MaintenanceIssue   `json:"issues,omitempty"`
	History []MaintenanceAttempt `json:"history,omitempty"`
}

type maintenancePlan struct {
	fault   FaultType
	target  Point
	service Point
	route   []Point
	exit    int
}

func (s *Sim) maintenanceSnapshot() *MaintenanceState {
	state := s.maintenance
	if state.Active == nil && len(state.History) == 0 && len(state.Issues) == 0 {
		return nil
	}
	if state.Active != nil {
		active := *state.Active
		state.Active = &active
	}
	state.Issues = slices.Clone(state.Issues)
	state.History = slices.Clone(state.History)
	return &state
}

func (s *Sim) activeFault(robot *RobotState) FaultState {
	if robot == nil || robot.ActiveFault == "" {
		return FaultState{}
	}
	for _, fault := range slices.Backward(s.state.Faults) {
		if fault.Type == robot.ActiveFault && len(fault.Robots) == 1 && fault.Robots[0] == robot.ID {
			return fault
		}
	}
	return FaultState{}
}

func (s *Sim) maintenanceRequests() []MaintenanceRequest {
	var active []FaultState
	for i := range s.state.Robots {
		if fault := s.activeFault(&s.state.Robots[i]); fault.ID != "" {
			active = append(active, fault)
		}
	}
	// All breakdown episodes are high severity. Oldest wins, then robot ID.
	slices.SortFunc(active, func(a, b FaultState) int {
		if order := cmp.Compare(a.Tick, b.Tick); order != 0 {
			return order
		}
		return CompareRobotIDs(a.Robots[0], b.Robots[0])
	})
	requests := make([]MaintenanceRequest, 0, len(active))
	for _, fault := range active {
		requests = append(requests, MaintenanceRequest{RobotID: fault.Robots[0], FaultID: fault.ID})
	}
	return requests
}

func repairEligibility(target *RobotState) MaintenanceReason {
	switch target.ActiveFault {
	case FaultMotorFailure, FaultSensorFailure:
		if target.Status == RobotFaulted {
			return ""
		}
	case FaultRobotUnresponsive:
		if target.Status == RobotOffline {
			return ""
		}
		return maintenanceAwaitingOffline
	case FaultCriticalBattery:
		return maintenanceExternalPower
	}
	return maintenanceTargetChanged
}

func (s *Sim) previewMaintenance(request MaintenanceRequest) (maintenancePlan, MaintenanceReason) {
	target := s.faultRobot(request.RobotID)
	if request.FaultID == "" || s.activeFault(target).ID != request.FaultID {
		return maintenancePlan{}, maintenanceTargetChanged
	}
	if target.Role != RoleTransport {
		return maintenancePlan{}, maintenanceSelfRepair
	}
	if issue := repairEligibility(target); issue != "" {
		return maintenancePlan{}, issue
	}
	for _, attempt := range s.maintenance.History {
		if attempt.FaultID == request.FaultID {
			return maintenancePlan{}, maintenanceAlreadyTried
		}
	}
	m1 := s.faultRobot(MaintenanceRobotID)
	if s.cfg.MaintenanceRepairTicks == 0 || s.maintenance.Active != nil || s.maintenance.Phase == MaintenanceReturning || !s.maintenanceReady(m1) {
		return maintenancePlan{}, maintenanceBusy
	}
	return s.planServiceCell(*target, *m1)
}

func (s *Sim) maintenanceReady(m1 *RobotState) bool {
	return m1 != nil && m1.Role == RoleMaintenance && m1.ActiveFault == "" && m1.Status == RobotIdle && m1.JobID == "" &&
		s.occupiedStation(m1.ID) == nil && s.reservedStation(m1.ID) == nil
}

func (s *Sim) assignMaintenance(request MaintenanceRequest) MaintenanceReason {
	plan, issue := s.previewMaintenance(request)
	if issue != "" {
		return issue
	}
	m1 := s.faultRobot(MaintenanceRobotID)
	s.clearNavigation(m1)
	s.navigation[m1.ID] = robotNavigation{target: plan.service, route: plan.route}
	m1.Status = RobotMoving
	s.maintenance.Active = &MaintenanceAttempt{
		MaintenanceRequest: request, ID: fmt.Sprintf("MA%d", len(s.maintenance.History)+1),
		JobID: s.faultRobot(request.RobotID).JobID,
		Fault: plan.fault, TargetCell: plan.target, ServiceCell: plan.service,
		StartedTick: s.state.Tick, RequiredRepairTicks: s.cfg.MaintenanceRepairTicks,
	}
	s.maintenanceDeadline = s.state.Tick + 2*len(plan.route) + 2*s.maintenanceRetryTicks()
	s.setMaintenancePhase(MaintenanceAssigned)
	return ""
}

// OperationsMaintenance previews current breakdowns without reserving M1 or an attempt.
func (s *Sim) OperationsMaintenance() ([]MaintenanceBreakdown, []MaintenanceOption) {
	breakdowns, options := []MaintenanceBreakdown{}, []MaintenanceOption{}
	for _, request := range s.maintenanceRequests() {
		target := s.faultRobot(request.RobotID)
		fault := s.activeFault(target)
		plan, reason := s.previewMaintenance(request)
		breakdowns = append(breakdowns, MaintenanceBreakdown{
			MaintenanceRequest: request, Fault: fault.Type, Tick: fault.Tick, Status: target.Status, Reason: reason,
		})
		if reason == "" {
			options = append(options, MaintenanceOption{
				MaintenanceRequest: request, JobID: target.JobID, ServiceCell: plan.service,
				TravelSteps: len(plan.route), ExitSteps: plan.exit, RepairTicks: s.cfg.MaintenanceRepairTicks,
				RequiredBattery: (len(plan.route)+plan.exit)*s.cfg.MovingDrain + s.cfg.MaintenanceRepairTicks + max(1, s.cfg.ReserveBattery),
			})
		}
	}
	return breakdowns, options
}

// DispatchMaintenance is COMMAND. The trusted caller serializes it with ticks.
func (s *Sim) DispatchMaintenance(request MaintenanceRequest) error {
	if !s.operationsMaintenanceHeld() {
		return ErrMaintenanceWindow
	}
	if reason := s.assignMaintenance(request); reason != "" {
		return fmt.Errorf("maintenance dispatch rejected: %s", reason)
	}
	s.maintenance.Active.AIDirected = true
	return nil
}

func (s *Sim) scheduleMaintenance() {
	if s.cfg.MaintenanceRepairTicks == 0 {
		return
	}
	m1 := s.faultRobot(MaintenanceRobotID)
	if s.maintenance.Active == nil {
		switch {
		case faultRobotUnavailable(m1):
			s.setMaintenancePhase(MaintenanceUnavailable)
		case s.maintenance.Phase == MaintenanceReturning && m1.Status != RobotCharging &&
			(Point{m1.X, m1.Y}) != *s.cfg.MaintenanceBay:
			s.prepareMaintenanceReturn(m1)
		case s.maintenanceReady(m1):
			s.setMaintenancePhase(MaintenanceAvailable)
		default:
			s.setMaintenancePhase(MaintenanceUnavailable)
		}
	}
	s.maintenance.Issues = s.maintenance.Issues[:0]
	for _, request := range s.maintenanceRequests() {
		if active := s.maintenance.Active; active != nil && active.FaultID == request.FaultID {
			continue
		}
		var issue MaintenanceReason
		if s.operationsMaintenanceHeld() {
			_, issue = s.previewMaintenance(request)
		} else {
			issue = s.assignMaintenance(request)
		}
		if issue != "" && issue != maintenanceAlreadyTried {
			s.maintenance.Issues = append(s.maintenance.Issues, MaintenanceIssue{MaintenanceRequest: request, Reason: issue})
		}
	}
}

func (s *Sim) maintenanceRetryTicks() int { return min(50, max(4, s.cfg.RecoverAfter)) }

func (s *Sim) setMaintenancePhase(phase MaintenancePhase) {
	if s.maintenance.Phase != phase {
		s.maintenance.Phase, s.maintenancePhaseTick = phase, s.state.Tick
	}
}

func (s *Sim) maintenanceTravel(robot RobotState) bool {
	return robot.ID == MaintenanceRobotID && s.maintenance.Active != nil &&
		(s.maintenance.Phase == MaintenanceAssigned || s.maintenance.Phase == MaintenanceTraveling)
}

func (s *Sim) stepMaintenanceTravel(m1 *RobotState) {
	if s.maintenanceTarget(s.maintenance.Active) == nil {
		s.finishMaintenance(m1, MaintenanceCancelled, maintenanceTargetChanged)
		return
	}
	if s.state.Tick == s.maintenance.Active.StartedTick {
		return
	}
	s.setMaintenancePhase(MaintenanceTraveling)
	service := s.maintenance.Active.ServiceCell
	if (Point{m1.X, m1.Y}) != service {
		if !s.maintenanceAffordable(*m1, service, len(s.navigation[m1.ID].route), s.cfg.MaintenanceRepairTicks) {
			s.waitForNavigation(m1, service)
			return
		}
		if !s.advanceNavigation(m1, service) {
			return
		}
		m1.Battery -= s.cfg.MovingDrain
	}
	if (Point{m1.X, m1.Y}) == service {
		m1.Status = RobotServicing
		s.clearNavigation(m1)
		s.setMaintenancePhase(MaintenanceAtTarget)
	}
}

func (s *Sim) maintenanceTarget(attempt *MaintenanceAttempt) *RobotState {
	target := s.faultRobot(attempt.RobotID)
	if s.activeFault(target).ID != attempt.FaultID || repairEligibility(target) != "" ||
		(Point{target.X, target.Y}) != attempt.TargetCell {
		return nil
	}
	return target
}

func (s *Sim) advanceMaintenance() {
	attempt := s.maintenance.Active
	if attempt == nil {
		return
	}
	m1 := s.faultRobot(MaintenanceRobotID)
	if faultRobotUnavailable(m1) {
		s.finishMaintenance(m1, MaintenanceFailed, maintenanceBusy)
		return
	}
	target := s.maintenanceTarget(attempt)
	if target == nil {
		s.finishMaintenance(m1, MaintenanceCancelled, maintenanceTargetChanged)
		return
	}
	if s.maintenanceTravel(*m1) {
		if s.state.Tick >= s.maintenanceDeadline {
			s.finishMaintenance(m1, MaintenanceUnresolved, maintenanceBlocked)
		}
		return
	}
	neighbors := orthogonalNeighbors(attempt.TargetCell)
	if m1.Status != RobotServicing || (Point{m1.X, m1.Y}) != attempt.ServiceCell || !slices.Contains(neighbors[:], attempt.ServiceCell) {
		s.finishMaintenance(m1, MaintenanceCancelled, maintenanceSiteLost)
		return
	}
	if s.state.Tick == s.maintenancePhaseTick {
		return
	}
	switch s.maintenance.Phase {
	case MaintenanceAtTarget:
		s.setMaintenancePhase(MaintenanceRepairing)
	case MaintenanceRepairing:
		remaining := attempt.RequiredRepairTicks - attempt.RepairTicks
		if !s.maintenanceAffordable(*m1, attempt.ServiceCell, 0, remaining) {
			s.finishMaintenance(m1, MaintenanceUnresolved, maintenanceInsufficient)
			return
		}
		m1.Battery--
		attempt.RepairTicks++
		if attempt.RepairTicks == attempt.RequiredRepairTicks {
			s.maintenanceDeadline = s.state.Tick + s.maintenanceRetryTicks()
			s.setMaintenancePhase(MaintenanceVerifying)
		}
	case MaintenanceVerifying:
		canExit := s.maintenanceAffordable(*m1, attempt.ServiceCell, 0, 0)
		if canExit && s.resumeRepairedRobot(target) {
			s.finishMaintenance(m1, MaintenanceCompleted, "")
		} else if s.state.Tick >= s.maintenanceDeadline {
			reason := maintenanceRecoveryBlocked
			if !canExit {
				reason = maintenanceInsufficient
			}
			s.finishMaintenance(m1, MaintenanceUnresolved, reason)
		}
	}
}

func (s *Sim) finishMaintenance(m1 *RobotState, outcome MaintenanceOutcome, reason MaintenanceReason) {
	attempt := *s.maintenance.Active
	attempt.FinishedTick, attempt.Outcome, attempt.Reason = s.state.Tick, outcome, reason
	s.maintenance.History = append(s.maintenance.History, attempt)
	s.maintenance.Active = nil
	s.clearNavigation(m1)
	if faultRobotUnavailable(m1) {
		s.setMaintenancePhase(MaintenanceUnavailable)
		return
	}
	m1.Status = RobotIdle
	s.setMaintenancePhase(MaintenanceReturning)
}

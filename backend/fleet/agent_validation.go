package fleet

func agentFacts(view StateView) (AgentFacts, error) {
	facts := AgentFacts{Tick: view.Tick, Poisoned: view.Poisoned, Candidates: []AgentCandidate{}, Incidents: []AgentIncident{}}
	if len(view.ReassignmentCandidates) > 64 || len(view.ActiveIncidents) > 64 {
		return facts, ErrAgentRecord
	}
	for _, i := range view.ActiveIncidents {
		if !agentID.MatchString(i.RobotID) || !allowed(i.Type, "traffic_blocked", "low_battery", "robot_unresponsive") || !allowed(i.Severity, "medium", "high") {
			return facts, ErrAgentRecord
		}
		facts.Incidents = append(facts.Incidents, AgentIncident{i.RobotID, i.Type, i.Severity})
		facts.Blocked = facts.Blocked || i.Type == "traffic_blocked"
	}
	for _, c := range view.ReassignmentCandidates {
		if !agentID.MatchString(c.JobID) || !agentID.MatchString(c.FromRobotID) || !agentID.MatchString(c.ToRobotID) {
			return facts, ErrAgentRecord
		}
		battery := 0
		for _, r := range view.Robots {
			if r.ID == c.ToRobotID {
				battery = r.Battery
			}
		}
		facts.Candidates = append(facts.Candidates, AgentCandidate{AgentKey{c.JobID, c.FromRobotID, c.ToRobotID}, map[string]int{
			"pickupSteps": c.PickupSteps, "deliverySteps": c.DeliverySteps, "chargingExitSteps": c.ChargingExitSteps,
			"totalRouteSteps": c.PickupSteps + c.DeliverySteps + c.ChargingExitSteps, "battery": battery, "requiredBattery": c.RequiredBattery,
			"missionHeadroom": battery - c.RequiredBattery, "admissionHeadroom": battery - max(30, c.RequiredBattery),
		}})
	}
	if len(canonicalAgentJSON(facts)) > 32768 {
		return facts, ErrAgentRecord
	}
	return facts, nil
}
func verifyAgentDecision(d *AgentDecision, facts AgentFacts, objective string) bool {
	if d == nil || len(d.Comparisons) > 128 {
		return false
	}
	candidates := map[AgentKey]AgentCandidate{}
	minPickup, maxHeadroom := int(^uint(0)>>1), -1
	for _, c := range facts.Candidates {
		candidates[c.Key] = c
		minPickup = min(minPickup, c.Metrics["pickupSteps"])
		maxHeadroom = max(maxHeadroom, c.Metrics["admissionHeadroom"])
	}
	coverage, ok := verifyAgentComparisons(d, candidates)
	if !ok {
		return false
	}
	if d.Kind == "abstain" {
		if d.Selected != nil || d.Basis != "" {
			return false
		}
		switch d.Reason {
		case "runtime_poisoned":
			return facts.Poisoned
		case "no_candidates":
			return !facts.Poisoned && facts.Blocked && len(candidates) == 0
		case "no_supported_incident":
			return !facts.Poisoned && !facts.Blocked && len(candidates) == 0
		case "operator_tradeoff":
			if objective != "compare_tradeoffs" || facts.Poisoned || len(candidates) == 0 {
				return false
			}
			for _, c := range candidates {
				if c.Metrics["pickupSteps"] == minPickup && c.Metrics["admissionHeadroom"] == maxHeadroom {
					return false
				}
			}
			return true
		}
		return false
	}
	if d.Kind != "select" || facts.Poisoned || d.Selected == nil || d.Reason != "" {
		return false
	}
	selected, ok := candidates[*d.Selected]
	if !ok {
		return false
	}
	if objective == "minimize_pickup_steps" && d.Basis != "pickup_cost" || objective == "preserve_battery_headroom" && d.Basis != "battery_headroom" {
		return false
	}
	for key := range candidates {
		if key != *d.Selected && (!coverage.covered[key] || d.Basis == "pickup_cost" && !coverage.pickupCovered[key] || d.Basis == "battery_headroom" && !coverage.batteryCovered[key]) {
			return false
		}
	}
	switch d.Basis {
	case "pickup_cost":
		return selected.Metrics["pickupSteps"] == minPickup
	case "battery_headroom":
		return selected.Metrics["admissionHeadroom"] == maxHeadroom
	case "admitted_option":
		return objective == "compare_tradeoffs"
	case "tradeoff":
		return objective == "compare_tradeoffs" && coverage.benefit && coverage.disadvantage
	}
	return false
}

type comparisonCoverage struct {
	covered, pickupCovered, batteryCovered map[AgentKey]bool
	benefit, disadvantage                  bool
}

func verifyAgentComparisons(d *AgentDecision, candidates map[AgentKey]AgentCandidate) (comparisonCoverage, bool) {
	coverage := comparisonCoverage{covered: map[AgentKey]bool{}, pickupCovered: map[AgentKey]bool{}, batteryCovered: map[AgentKey]bool{}}
	seen := map[struct {
		Left, Right AgentKey
		Metric      string
	}]bool{}
	for _, c := range d.Comparisons {
		left, lok := candidates[c.Left]
		right, rok := candidates[c.Right]
		lv, metric := left.Metrics[c.Metric]
		rv := right.Metrics[c.Metric]
		key := struct {
			Left, Right AgentKey
			Metric      string
		}{c.Left, c.Right, c.Metric}
		if !lok || !rok || !metric || c.Left == c.Right || seen[key] || c.LeftValue != lv || c.RightValue != rv {
			return coverage, false
		}
		seen[key] = true
		relation := "equal"
		if lv < rv {
			relation = "less"
		} else if lv > rv {
			relation = "greater"
		}
		if relation != c.Relation {
			return coverage, false
		}
		if d.Selected != nil && (c.Left == *d.Selected || c.Right == *d.Selected) {
			other := c.Right
			selectedValue, otherValue := lv, rv
			if c.Right == *d.Selected {
				other = c.Left
				selectedValue, otherValue = rv, lv
			}
			coverage.covered[other] = true
			if c.Metric == "pickupSteps" {
				coverage.pickupCovered[other] = true
			}
			if c.Metric == "admissionHeadroom" {
				coverage.batteryCovered[other] = true
			}
			higherBetter := allowed(c.Metric, "battery", "missionHeadroom", "admissionHeadroom")
			coverage.benefit = coverage.benefit || (selectedValue > otherValue && higherBetter) || (selectedValue < otherValue && !higherBetter)
			coverage.disadvantage = coverage.disadvantage || (selectedValue < otherValue && higherBetter) || (selectedValue > otherValue && !higherBetter)
		}
	}
	return coverage, true
}

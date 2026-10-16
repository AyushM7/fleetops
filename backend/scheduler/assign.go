// Package scheduler provides deterministic greedy job assignment.
package scheduler

import (
	"cmp"
	"slices"
)

type Cell struct{ X, Y int }

type RobotInfo struct {
	Num     int
	Pos     Cell
	Battery int
}

type JobInfo struct {
	Num      int
	From, To Cell
	ExitCost int
}

type Assignment struct{ JobNum, RobotNum int }

type Params struct{ MinBattery, ReserveBattery, MovingDrain int }

// Distance reports route length and whether the destination is reachable.
type Distance func(a, b Cell) (int, bool)

// Assign processes jobs by numeric ID, choosing the nearest feasible unused robot.
// This is greedy assignment rather than global optimal matching.
func Assign(idle []RobotInfo, pending []JobInfo, dist Distance, p Params) []Assignment {
	if len(idle) == 0 || len(pending) == 0 {
		return nil
	}
	jobs := slices.Clone(pending)
	slices.SortFunc(jobs, func(a, b JobInfo) int { return cmp.Compare(a.Num, b.Num) })
	used := make(map[int]bool, len(idle))
	var assignments []Assignment
	for _, job := range jobs {
		if job.ExitCost < 0 {
			continue
		}
		delivery, reachable := dist(job.From, job.To)
		if !reachable {
			continue
		}
		index := nearestRobot(idle, used, job.From, delivery+job.ExitCost, dist, p)
		if index < 0 {
			continue
		}
		robot := idle[index]
		used[robot.Num] = true
		assignments = append(assignments, Assignment{JobNum: job.Num, RobotNum: robot.Num})
	}
	return assignments
}

func nearestRobot(idle []RobotInfo, used map[int]bool, pickup Cell, delivery int, dist Distance, p Params) int {
	best, shortest := -1, 0
	for i, robot := range idle {
		if used[robot.Num] || robot.Battery < p.MinBattery {
			continue
		}
		leg1, reachable := dist(robot.Pos, pickup)
		if !reachable || robot.Battery < (leg1+delivery)*p.MovingDrain+p.ReserveBattery {
			continue
		}
		if best < 0 || leg1 < shortest || leg1 == shortest && robot.Num < idle[best].Num {
			best, shortest = i, leg1
		}
	}
	return best
}

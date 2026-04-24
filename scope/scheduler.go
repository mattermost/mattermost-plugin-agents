// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scope

import (
	"context"
	"time"

	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// ScheduleClusterKey uniquely identifies the scheduler's cluster.Schedule
// job. Only one node in a Mattermost HA cluster holds the lock at any
// instant, so the tick callback runs at most once per interval regardless of
// how many plugin nodes are active.
const ScheduleClusterKey = "agents-scheduler-tick"

// ScheduleTickInterval is the cadence at which we scan persisted schedules
// for due fires. A single tick handles every agent schedule across the
// cluster, so this interval bounds both scheduler latency and DB load.
const ScheduleTickInterval = 60 * time.Second

// Scheduler drives recurring scoped runs. It registers ONE cluster.Schedule
// job that ticks every ScheduleTickInterval; inside the callback it scans
// active schedules and dispatches those whose NextFireAt has arrived.
//
// This is intentionally not N cluster.Schedule jobs (one per agent). The
// existing cluster.Schedule primitive already provides HA leader election
// via a KV-mutex; layering a single-tick scan on top trades 60s worst-case
// latency for O(1) cluster-lock churn as agents come and go.
type Scheduler struct {
	pluginJobAPI cluster.JobPluginAPI
	store        ScheduleStore
	dispatcher   ScheduleDispatcher
	log          Logger

	job *cluster.Job
}

// ScheduleStore lists active agents and records schedule fire state.
type ScheduleStore interface {
	AgentLister
	UpdateAgentScheduleState(agentID, scheduleID string, nextFireAt, lastFireAt, expectedNextFireAt int64) error
}

// ScheduleDispatcher fires a due schedule.
type ScheduleDispatcher interface {
	DispatchSchedule(ctx context.Context, agentID string, sched llm.AgentSchedule, firedAt time.Time)
}

// NewScheduler wires up a Scheduler. Call Start to register the job.
func NewScheduler(
	pluginJobAPI cluster.JobPluginAPI,
	store ScheduleStore,
	dispatcher ScheduleDispatcher,
	log Logger,
) *Scheduler {
	return &Scheduler{
		pluginJobAPI: pluginJobAPI,
		store:        store,
		dispatcher:   dispatcher,
		log:          log,
	}
}

// Start registers the recurring cluster.Schedule job. Calling Start twice is
// a no-op if the first call succeeded.
func (s *Scheduler) Start() error {
	if s.job != nil {
		return nil
	}
	job, err := cluster.Schedule(
		s.pluginJobAPI,
		ScheduleClusterKey,
		cluster.MakeWaitForRoundedInterval(ScheduleTickInterval),
		s.tick,
	)
	if err != nil {
		return err
	}
	s.job = job
	return nil
}

// Close releases the cluster lock and stops future ticks.
func (s *Scheduler) Close() error {
	if s.job == nil {
		return nil
	}
	err := s.job.Close()
	s.job = nil
	return err
}

// tick scans persisted agents for schedules that are due, advances each due
// schedule's persisted fire state, then dispatches the scoped run.
func (s *Scheduler) tick() {
	s.tickAt(time.Now())
}

func (s *Scheduler) tickAt(now time.Time) {
	agents, err := s.store.ListAgents()
	if err != nil {
		s.log.Error("scheduler: failed to list agents", "error", err.Error())
		return
	}
	ctx := context.Background()

	for _, cfg := range agents {
		if cfg == nil || cfg.DeleteAt != 0 {
			continue
		}
		for i := range cfg.Schedules {
			sched := cfg.Schedules[i]
			if !sched.Enabled {
				continue
			}
			if sched.IntervalSeconds < llm.MinScheduleIntervalSeconds {
				continue
			}
			if !dueNow(sched, now) {
				continue
			}
			if sched.ID == "" {
				s.log.Error("scheduler: due schedule has no ID", "agent", cfg.ID)
				continue
			}
			nextFireAt := NextFireBucket(sched.IntervalSeconds, now)
			lastFireAt := now.Unix()
			if err := s.store.UpdateAgentScheduleState(cfg.ID, sched.ID, nextFireAt, lastFireAt, sched.NextFireAt); err != nil {
				s.log.Error("scheduler: failed to advance schedule", "agent", cfg.ID, "schedule", sched.ID, "error", err.Error())
				continue
			}
			sched.LastFireAt = lastFireAt
			sched.NextFireAt = nextFireAt
			s.dispatcher.DispatchSchedule(ctx, cfg.ID, sched, now)
		}
	}
}

// dueNow reports whether sched should fire at now. A schedule fires when the
// wall clock has passed the epoch-aligned bucket boundary. Restart-safe
// because the bucket is a pure function of IntervalSeconds and wall-clock.
func dueNow(sched llm.AgentSchedule, now time.Time) bool {
	if sched.IntervalSeconds <= 0 {
		return false
	}
	// If NextFireAt is unset, compute the next bucket and defer firing until
	// we're past it. This prevents an avalanche of immediate fires right after
	// a schedule is created.
	if sched.NextFireAt == 0 {
		return false
	}
	return now.Unix() >= sched.NextFireAt
}

// NextFireBucket computes the next epoch-aligned fire timestamp after now,
// given IntervalSeconds. Exposed for the API layer to initialize NextFireAt
// when a schedule is first created or enabled.
func NextFireBucket(intervalSeconds int64, now time.Time) int64 {
	if intervalSeconds <= 0 {
		return 0
	}
	nowSec := now.Unix()
	bucket := ((nowSec / intervalSeconds) + 1) * intervalSeconds
	return bucket
}

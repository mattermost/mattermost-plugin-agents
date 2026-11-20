// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package projects

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

const checkinTickInterval = time.Minute

// maxScheduleSearch bounds how far ahead Next looks for a matching minute.
const maxScheduleSearch = 366 * 24 * time.Hour

// Schedule is a five-field cron schedule (minute hour day-of-month month
// day-of-week) in a time zone.
type Schedule struct {
	minute, hour, dom, month, dow []bool
	domAny, dowAny                bool
	loc                           *time.Location
}

// ParseSchedule parses a five-field cron expression. Fields accept *, numbers,
// lists, ranges, and steps. An empty timezone means UTC.
func ParseSchedule(expr, timezone string) (*Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, errors.New("a schedule needs five fields: minute hour day month weekday")
	}
	loc := time.UTC
	if timezone != "" {
		l, err := time.LoadLocation(timezone)
		if err != nil {
			return nil, fmt.Errorf("unknown time zone %q", timezone)
		}
		loc = l
	}
	s := &Schedule{loc: loc, domAny: fields[2] == "*", dowAny: fields[4] == "*"}
	var err error
	if s.minute, err = parseField(fields[0], 0, 59); err != nil {
		return nil, err
	}
	if s.hour, err = parseField(fields[1], 0, 23); err != nil {
		return nil, err
	}
	if s.dom, err = parseField(fields[2], 1, 31); err != nil {
		return nil, err
	}
	if s.month, err = parseField(fields[3], 1, 12); err != nil {
		return nil, err
	}
	if s.dow, err = parseField(fields[4], 0, 7); err != nil {
		return nil, err
	}
	if s.dow[7] {
		s.dow[0] = true
	}
	return s, nil
}

func parseField(field string, lo, hi int) ([]bool, error) {
	set := make([]bool, hi+1)
	for part := range strings.SplitSeq(field, ",") {
		rangePart, step := part, 1
		if before, after, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(after)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("invalid step in %q", field)
			}
			rangePart, step = before, n
		}
		start, end := lo, hi
		if rangePart != "*" {
			if a, b, ok := strings.Cut(rangePart, "-"); ok {
				var err1, err2 error
				start, err1 = strconv.Atoi(a)
				end, err2 = strconv.Atoi(b)
				if err1 != nil || err2 != nil {
					return nil, fmt.Errorf("invalid range in %q", field)
				}
			} else {
				n, err := strconv.Atoi(rangePart)
				if err != nil {
					return nil, fmt.Errorf("invalid value in %q", field)
				}
				start = n
				if !strings.Contains(part, "/") {
					end = n
				}
			}
		}
		if start < lo || end > hi || start > end {
			return nil, fmt.Errorf("value out of range in %q", field)
		}
		for v := start; v <= end; v += step {
			set[v] = true
		}
	}
	return set, nil
}

// Next returns the first minute strictly after from that matches.
func (s *Schedule) Next(from time.Time) (time.Time, bool) {
	t := from.In(s.loc).Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(maxScheduleSearch)
	for t.Before(limit) {
		if s.matches(t) {
			return t, true
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, false
}

func (s *Schedule) matches(t time.Time) bool {
	if !s.minute[t.Minute()] || !s.hour[t.Hour()] || !s.month[int(t.Month())] {
		return false
	}
	domMatch, dowMatch := s.dom[t.Day()], s.dow[int(t.Weekday())]
	switch {
	case s.domAny && s.dowAny:
		return true
	case s.domAny:
		return dowMatch
	case s.dowAny:
		return domMatch
	default:
		return domMatch || dowMatch
	}
}

// StartCheckinTicker runs due check-ins every minute until
// StopCheckinTicker. Each check-in is claimed in the database, so on a
// cluster only one node runs it.
func (s *Service) StartCheckinTicker() {
	s.tickerMu.Lock()
	defer s.tickerMu.Unlock()
	if s.tickerStop != nil {
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	s.tickerStop, s.tickerDone = stop, done
	go func() {
		defer close(done)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ticker := time.NewTicker(checkinTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.RunDueCheckins(ctx, time.Now())
			}
		}
	}()
}

// StopCheckinTicker stops the ticker and waits for it to finish.
func (s *Service) StopCheckinTicker() {
	s.tickerMu.Lock()
	stop, done := s.tickerStop, s.tickerDone
	s.tickerStop, s.tickerDone = nil, nil
	s.tickerMu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

// RunDueCheckins claims and starts every check-in due at now.
func (s *Service) RunDueCheckins(ctx context.Context, now time.Time) {
	if !s.enabled() {
		return
	}
	due, err := s.deps.Store.DueCheckins(now.UnixMilli())
	if err != nil {
		s.deps.Platform.LogError("Failed to list due project check-ins", "error", err)
		return
	}
	for _, project := range due {
		if err := s.runCheckin(ctx, project, now); err != nil {
			s.deps.Platform.LogError("Failed to run project check-in", "channel_id", project.ChannelID, "error", err)
		}
	}
}

func (s *Service) runCheckin(ctx context.Context, project *store.Project, now time.Time) error {
	var nextAt int64
	if schedule, err := ParseSchedule(project.CheckinCron, project.CheckinTimezone); err == nil {
		if next, ok := schedule.Next(now); ok {
			nextAt = next.UnixMilli()
		}
	}
	claimed, err := s.deps.Store.ClaimCheckin(project.ChannelID, project.NextCheckinAt, nextAt, now.UnixMilli())
	if err != nil || !claimed {
		return err
	}

	lead, err := s.checkinLead(project)
	if err != nil {
		s.pauseCheckins(project, "Scheduled check-ins are paused because the project lead is no longer a member who can run them. Transfer the lead to resume them.")
		return nil
	}
	bot := s.deps.Bots.GetBotByID(project.AgentBotUserID)
	if bot == nil || bot.GetConfig().ID != project.AgentID {
		return errors.New("the project agent is not running")
	}
	channel, err := s.deps.Platform.GetChannel(project.ChannelID)
	if err != nil {
		return fmt.Errorf("failed to get the project channel: %w", err)
	}
	thread, err := s.deps.Threads.StartAgentThread(ctx, conversations.AgentThreadRequest{
		Bot:         bot,
		User:        lead,
		Channel:     channel,
		Operation:   llm.OperationProjectCheckin,
		Message:     "Project check-in",
		Instruction: checkinPrompt,
		RootProps:   map[string]any{conversations.ProjectThreadOperationProp: llm.OperationProjectCheckin},
	})
	if err != nil {
		return fmt.Errorf("failed to start the check-in: %w", err)
	}
	return s.deps.Store.SetProjectLastCheckinPost(project.ChannelID, thread.RootPost.Id)
}

// checkinLead returns the lead a check-in runs on behalf of, failing when
// the lead can no longer act for the project.
func (s *Service) checkinLead(project *store.Project) (*model.User, error) {
	lead, err := s.deps.Platform.GetUser(project.LeadUserID)
	if err != nil {
		return nil, err
	}
	if lead.DeleteAt != 0 || lead.IsGuest() || lead.IsBot || !s.isMember(project.ChannelID, lead.Id) {
		return nil, ErrForbidden
	}
	return lead, nil
}

func (s *Service) pauseCheckins(project *store.Project, message string) {
	if err := s.deps.Store.SetProjectSchedule(project.ChannelID, project.CheckinCron, project.CheckinTimezone, 0); err != nil {
		s.deps.Platform.LogError("Failed to pause project check-ins", "channel_id", project.ChannelID, "error", err)
		return
	}
	if err := s.deps.Platform.CreatePost(&model.Post{ChannelId: project.ChannelID, UserId: project.AgentBotUserID, Message: message}); err != nil {
		s.deps.Platform.LogError("Failed to post paused check-ins notice", "channel_id", project.ChannelID, "error", err)
	}
	s.broadcast(project.ChannelID)
}

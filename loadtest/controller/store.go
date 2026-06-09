// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package controller

import (
	"sync"
)

// PluginStore holds mutex-protected per-loadtest-user state for the simulated controller.
type PluginStore struct {
	lock  sync.RWMutex
	users map[string]UserState
}

// UserState is cached context for one simulated user.
type UserState struct {
	AgentUserID      string
	AgentUsername    string
	CurrentTeamID    string
	CurrentChannelID string
	PromptCounter    int64
}

// AgentTarget resolves the Agents bot user for mentions and DMs.
type AgentTarget struct {
	UserID   string
	Username string
}

// NewPluginStore returns an empty PluginStore.
func NewPluginStore() *PluginStore {
	return &PluginStore{
		users: make(map[string]UserState),
	}
}

// Clear removes all users' state.
func (s *PluginStore) Clear() {
	s.lock.Lock()
	defer s.lock.Unlock()
	clear(s.users)
}

// Get returns a copy of state for userID.
func (s *PluginStore) Get(userID string) UserState {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.users[userID]
}

// SetAgentTarget stores resolved agent identity for userID.
func (s *PluginStore) SetAgentTarget(userID string, target AgentTarget) {
	s.lock.Lock()
	defer s.lock.Unlock()
	st := s.users[userID]
	st.AgentUserID = target.UserID
	st.AgentUsername = target.Username
	s.users[userID] = st
}

// SetCurrentTeam records the active team for userID.
func (s *PluginStore) SetCurrentTeam(userID, teamID string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	st := s.users[userID]
	st.CurrentTeamID = teamID
	s.users[userID] = st
}

// SetCurrentChannel records the active channel for userID.
func (s *PluginStore) SetCurrentChannel(userID, channelID string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	st := s.users[userID]
	st.CurrentChannelID = channelID
	s.users[userID] = st
}

// NextPromptCounter returns a monotonically increasing prompt sequence number per user.
func (s *PluginStore) NextPromptCounter(userID string) int64 {
	s.lock.Lock()
	defer s.lock.Unlock()
	st := s.users[userID]
	st.PromptCounter++
	n := st.PromptCounter
	s.users[userID] = st
	return n
}

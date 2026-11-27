// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package dictation

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const testUserID = "user12345678901234567890ab"

func setConfig(c *config.Container, d config.DictationConfig, services ...llm.ServiceConfig) {
	c.Update(&config.Config{Services: services, Dictation: d})
}

func openAIService(id, key string) llm.ServiceConfig {
	return llm.ServiceConfig{ID: id, Name: id, Type: llm.ServiceTypeOpenAI, APIKey: key}
}

func enabled(serviceID string) config.DictationConfig {
	return config.DictationConfig{Enabled: true, ServiceID: serviceID, Model: "gpt-live-transcribe"}
}

type levelChecker struct{ level enterprise.Level }

func (c levelChecker) Check(capability enterprise.Capability) error {
	if c.level >= enterprise.RequiredLevel(capability) {
		return nil
	}
	return enterprise.NewLicenseError(capability, c.level)
}

type accessFunc func(ctx context.Context, userID, serviceID string) error

func (f accessFunc) CanUseService(ctx context.Context, userID, serviceID string) error {
	return f(ctx, userID, serviceID)
}

var allowAll = accessFunc(func(context.Context, string, string) error { return nil })

type usersFunc func(string) (*model.User, error)

func (f usersFunc) GetUser(id string) (*model.User, error) { return f(id) }

var regularUser = usersFunc(func(id string) (*model.User, error) {
	return &model.User{Id: id, Roles: model.SystemUserRoleId}, nil
})

type nopLogger struct{}

func (nopLogger) Info(string, ...any) {}
func (nopLogger) Warn(string, ...any) {}

// memoryLeases is a LeaseStore with the KV store's compare-and-set
// semantics, shared by every Service in a test like the KV store is by
// every node.
type memoryLeases struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newMemoryLeases() *memoryLeases { return &memoryLeases{values: map[string][]byte{}} }

func (m *memoryLeases) Get(key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.values[key], nil
}

func (m *memoryLeases) CompareAndSet(key string, old, value []byte, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, exists := m.values[key]
	if (old == nil && exists) || (old != nil && !bytes.Equal(current, old)) {
		return false, nil
	}
	m.values[key] = value
	return true, nil
}

func TestNilDependenciesFailClosed(t *testing.T) {
	tests := []struct {
		name       string
		license    LicenseChecker
		access     AccessChecker
		users      UserGetter
		leases     LeaseStore
		wantReason string
		wantErr    error
	}{
		{name: "nil license checker", access: allowAll, users: regularUser, leases: newMemoryLeases(), wantReason: ReasonLicense, wantErr: enterprise.ErrNotLicensed},
		{name: "nil access checker", license: levelChecker{enterprise.LevelEnterprise}, users: regularUser, leases: newMemoryLeases(), wantReason: ReasonUnavailable, wantErr: ErrAccessDenied},
		{name: "nil user getter", license: levelChecker{enterprise.LevelEnterprise}, access: allowAll, leases: newMemoryLeases(), wantReason: ReasonUnavailable, wantErr: ErrAccessDenied},
		{name: "nil lease store", license: levelChecker{enterprise.LevelEnterprise}, access: allowAll, users: regularUser, wantErr: ErrMisconfigured},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Container{}
			setConfig(cfg, enabled("openai1"), openAIService("openai1", "k"))
			svc := New(cfg, tt.license, tt.access, tt.users, tt.leases, nopLogger{})
			built := 0
			svc.newClient = func(llm.ServiceConfig) (callClient, error) { built++; return nil, assert.AnError }

			if tt.wantReason != "" {
				avail := svc.Availability(context.Background(), testUserID)
				assert.False(t, avail.Available)
				assert.Equal(t, tt.wantReason, avail.Reason)
			}
			session, err := svc.CreateSession(context.Background(), testUserID, "v=0\r\n")
			assert.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, session)
			assert.Zero(t, built, "no call is attempted for a denied request")
		})
	}
}

func TestSessionLeases(t *testing.T) {
	store := newMemoryLeases()
	clock := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	// Two nodes share the store, as they share the KV store.
	nodeA := &sessionLeases{store: store, max: 2, leaseTTL: 11 * time.Minute, now: now}
	nodeB := &sessionLeases{store: store, max: 2, leaseTTL: 11 * time.Minute, now: now}

	require.NoError(t, nodeA.acquire(testUserID, "s1"))
	require.NoError(t, nodeB.acquire(testUserID, "s2"))
	assert.ErrorIs(t, nodeA.acquire(testUserID, "s3"), errConcurrentLimit, "the cap holds across nodes")
	require.NoError(t, nodeA.acquire("othe12345678901234567890ab", "s4"), "the cap is per user")

	require.NoError(t, nodeA.release(testUserID, "s2"), "any node can release a lease")
	require.NoError(t, nodeB.acquire(testUserID, "s5"))
	assert.ErrorIs(t, nodeB.acquire(testUserID, "s6"), errConcurrentLimit)

	// A node that stops without releasing leaves leases that expire by this
	// server's clock.
	clock = clock.Add(11*time.Minute + time.Second)
	require.NoError(t, nodeA.acquire(testUserID, "s7"))
}

func TestSessionLeasesConcurrentAcquire(t *testing.T) {
	store := newMemoryLeases()
	leases := &sessionLeases{store: store, max: 2, leaseTTL: time.Minute, now: time.Now}

	var wg sync.WaitGroup
	var mu sync.Mutex
	acquired := 0
	for range 8 {
		wg.Go(func() {
			if leases.acquire(testUserID, model.NewId()) == nil {
				mu.Lock()
				acquired++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	assert.LessOrEqual(t, acquired, 2, "compare-and-set keeps the cap under races")
}

func TestRateLimiter(t *testing.T) {
	clock := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	l := newRateLimiter(sessionStartsPerWindow, sessionStartWindow)
	l.now = func() time.Time { return clock }

	for range sessionStartsPerWindow {
		require.True(t, l.allow(testUserID))
		clock = clock.Add(time.Second)
	}
	assert.False(t, l.allow(testUserID))
	assert.True(t, l.allow("othe12345678901234567890ab"), "limits are per user")

	clock = clock.Add(sessionStartWindow)
	assert.True(t, l.allow(testUserID))
}

// blockingLicense blocks its first Check until released, to hold one
// Reconcile in the middle of reading state.
type blockingLicense struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (b *blockingLicense) Check(enterprise.Capability) error {
	b.once.Do(func() {
		close(b.entered)
		<-b.release
	})
	return nil
}

func TestConcurrentReconcilesKeepTheLatestState(t *testing.T) {
	cfg := &config.Container{}
	setConfig(cfg, enabled("openai1"), openAIService("openai1", "k"))
	license := &blockingLicense{entered: make(chan struct{}), release: make(chan struct{})}
	svc := New(cfg, nil, allowAll, regularUser, newMemoryLeases(), nopLogger{})
	svc.license = license

	slowDone := make(chan struct{})
	go func() {
		defer close(slowDone)
		svc.Reconcile() // reads "enabled", then blocks in the license check
	}()
	<-license.entered

	setConfig(cfg, config.DictationConfig{}, openAIService("openai1", "k"))
	fastDone := make(chan struct{})
	go func() {
		defer close(fastDone)
		svc.Reconcile() // reads "disabled"
	}()
	select {
	case <-fastDone:
	case <-time.After(100 * time.Millisecond):
	}
	close(license.release)
	<-slowDone
	<-fastDone

	svc.mu.Lock()
	defer svc.mu.Unlock()
	assert.False(t, svc.siteState.Enabled, "the state from the earlier config must not overwrite the later one")
}

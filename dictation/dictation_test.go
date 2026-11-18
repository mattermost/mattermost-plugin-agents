// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package dictation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const testUserID = "user12345678901234567890ab"

type fakeConfig struct {
	mu        sync.Mutex
	dictation config.DictationConfig
	services  []llm.ServiceConfig
}

func (c *fakeConfig) Dictation() config.DictationConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dictation
}

func (c *fakeConfig) GetServiceByID(id string) (llm.ServiceConfig, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, svc := range c.services {
		if svc.ID == id {
			return svc, true
		}
	}
	return llm.ServiceConfig{}, false
}

func (c *fakeConfig) set(d config.DictationConfig, services ...llm.ServiceConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dictation = d
	c.services = services
}

type levelChecker struct{ level atomic.Int32 }

func newLevelChecker(level enterprise.Level) *levelChecker {
	c := &levelChecker{}
	c.level.Store(int32(level))
	return c
}

func (c *levelChecker) Check(capability enterprise.Capability) error {
	level := enterprise.Level(c.level.Load())
	if level >= enterprise.RequiredLevel(capability) {
		return nil
	}
	return enterprise.NewLicenseError(capability, level)
}

type accessFunc func(ctx context.Context, userID, serviceID string) error

func (f accessFunc) CanUseService(ctx context.Context, userID, serviceID string) error {
	return f(ctx, userID, serviceID)
}

var allowAll = accessFunc(func(context.Context, string, string) error { return nil })

type nopLogger struct{}

func (nopLogger) Info(string, ...any) {}
func (nopLogger) Warn(string, ...any) {}

type fakeMinter struct {
	svc      llm.ServiceConfig
	err      error
	shutdown chan struct{}
}

func (m *fakeMinter) CreateTranscriptionSession(_ context.Context, req bifrost.TranscriptionSessionRequest) (*bifrost.TranscriptionSession, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &bifrost.TranscriptionSession{
		ClientSecret: "ek_" + m.svc.ID,
		ExpiresAt:    time.Now().Add(time.Minute).UnixMilli(),
		Provider:     m.svc.Type,
		Model:        req.Model,
	}, nil
}

func (m *fakeMinter) Shutdown() { close(m.shutdown) }

type minterFactory struct {
	mu      sync.Mutex
	built   []*fakeMinter
	mintErr error
}

func (f *minterFactory) build(svc llm.ServiceConfig) (sessionMinter, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := &fakeMinter{svc: svc, err: f.mintErr, shutdown: make(chan struct{})}
	f.built = append(f.built, m)
	return m, nil
}

func openAIService(id, key string) llm.ServiceConfig {
	return llm.ServiceConfig{ID: id, Name: id, Type: llm.ServiceTypeOpenAI, APIKey: key}
}

func enabled(serviceID string) config.DictationConfig {
	return config.DictationConfig{Enabled: true, ServiceID: serviceID, Model: "gpt-live-transcribe"}
}

func newTestService(cfg *fakeConfig, license LicenseChecker, access AccessChecker) (*Service, *minterFactory) {
	factory := &minterFactory{}
	svc := New(cfg, license, access, nopLogger{})
	svc.newMinter = factory.build
	return svc, factory
}

func TestAvailabilityAndCreateSessionOutcomes(t *testing.T) {
	denyAll := accessFunc(func(context.Context, string, string) error { return errors.New("denied by policy") })

	tests := []struct {
		name       string
		dictation  config.DictationConfig
		services   []llm.ServiceConfig
		level      enterprise.Level
		access     AccessChecker
		noLicense  bool
		wantReason string
		wantErr    error
	}{
		{
			name:       "disabled",
			dictation:  config.DictationConfig{ServiceID: "openai1", Model: "gpt-live-transcribe"},
			services:   []llm.ServiceConfig{openAIService("openai1", "k")},
			level:      enterprise.LevelEnterprise,
			access:     allowAll,
			wantReason: ReasonDisabled,
			wantErr:    ErrDisabled,
		},
		{
			name:       "professional license",
			dictation:  enabled("openai1"),
			services:   []llm.ServiceConfig{openAIService("openai1", "k")},
			level:      enterprise.LevelProfessional,
			access:     allowAll,
			wantReason: ReasonLicense,
			wantErr:    enterprise.ErrNotLicensed,
		},
		{
			name:       "nil license checker fails closed",
			dictation:  enabled("openai1"),
			services:   []llm.ServiceConfig{openAIService("openai1", "k")},
			access:     allowAll,
			noLicense:  true,
			wantReason: ReasonLicense,
			wantErr:    enterprise.ErrNotLicensed,
		},
		{
			name:       "service deleted",
			dictation:  enabled("openai1"),
			level:      enterprise.LevelEnterprise,
			access:     allowAll,
			wantReason: ReasonMisconfigured,
			wantErr:    ErrMisconfigured,
		},
		{
			name:       "service of the wrong type",
			dictation:  enabled("anthropic1"),
			services:   []llm.ServiceConfig{{ID: "anthropic1", Type: llm.ServiceTypeAnthropic, APIKey: "k"}},
			level:      enterprise.LevelEnterprise,
			access:     allowAll,
			wantReason: ReasonMisconfigured,
			wantErr:    ErrMisconfigured,
		},
		{
			name:       "access denied",
			dictation:  enabled("openai1"),
			services:   []llm.ServiceConfig{openAIService("openai1", "k")},
			level:      enterprise.LevelEnterprise,
			access:     denyAll,
			wantReason: ReasonAccessDenied,
			wantErr:    ErrAccessDenied,
		},
		{
			name:       "nil access checker fails closed",
			dictation:  enabled("openai1"),
			services:   []llm.ServiceConfig{openAIService("openai1", "k")},
			level:      enterprise.LevelEnterprise,
			wantReason: ReasonAccessDenied,
			wantErr:    ErrAccessDenied,
		},
		{
			name:      "available at enterprise advanced",
			dictation: enabled("openai1"),
			services:  []llm.ServiceConfig{openAIService("openai1", "k")},
			level:     enterprise.LevelEnterpriseAdvanced,
			access:    allowAll,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &fakeConfig{}
			cfg.set(tt.dictation, tt.services...)
			var license LicenseChecker
			if !tt.noLicense {
				license = newLevelChecker(tt.level)
			}
			svc, factory := newTestService(cfg, license, tt.access)

			avail, err := svc.Availability(context.Background(), testUserID)
			require.NoError(t, err)
			assert.Equal(t, tt.dictation.Enabled, avail.Enabled)
			assert.Equal(t, TransportWebRTC, avail.Transport)
			assert.Equal(t, tt.wantReason, avail.Reason)
			assert.Equal(t, tt.wantReason == "", avail.Available)

			session, err := svc.CreateSession(context.Background(), testUserID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, session)
				assert.Empty(t, factory.built, "no provider client is built for a denied request")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, llm.ServiceTypeOpenAI, avail.Provider)
			assert.Equal(t, "gpt-live-transcribe", avail.Model)
			assert.Equal(t, "ek_openai1", session.ClientSecret)
		})
	}
}

func TestLicenseDowngradeAppliesImmediately(t *testing.T) {
	cfg := &fakeConfig{}
	cfg.set(enabled("openai1"), openAIService("openai1", "k"))
	license := newLevelChecker(enterprise.LevelEnterprise)
	svc, factory := newTestService(cfg, license, allowAll)

	_, err := svc.CreateSession(context.Background(), testUserID)
	require.NoError(t, err)

	license.level.Store(int32(enterprise.LevelProfessional))
	svc.Reconcile()

	avail, err := svc.Availability(context.Background(), testUserID)
	require.NoError(t, err)
	assert.False(t, avail.Available)
	assert.Equal(t, ReasonLicense, avail.Reason)

	_, err = svc.CreateSession(context.Background(), testUserID)
	assert.ErrorIs(t, err, enterprise.ErrNotLicensed)

	require.Len(t, factory.built, 1)
	assertShutdown(t, factory.built[0])
}

func TestMinterRebuiltOnServiceChange(t *testing.T) {
	cfg := &fakeConfig{}
	cfg.set(enabled("openai1"), openAIService("openai1", "key-1"))
	svc, factory := newTestService(cfg, newLevelChecker(enterprise.LevelEnterprise), allowAll)

	_, err := svc.CreateSession(context.Background(), testUserID)
	require.NoError(t, err)
	_, err = svc.CreateSession(context.Background(), "othe12345678901234567890ab")
	require.NoError(t, err)
	require.Len(t, factory.built, 1, "an unchanged service reuses its client")

	svc.Reconcile()
	require.Len(t, factory.built, 1)
	assertNotShutdown(t, factory.built[0])

	cfg.set(enabled("openai1"), openAIService("openai1", "key-2"))
	svc.Reconcile()
	assertShutdown(t, factory.built[0])

	session, err := svc.CreateSession(context.Background(), "thrd12345678901234567890ab")
	require.NoError(t, err)
	require.Len(t, factory.built, 2)
	assert.Equal(t, "key-2", factory.built[1].svc.APIKey)
	assert.Equal(t, "ek_openai1", session.ClientSecret)

	cfg.set(config.DictationConfig{}, openAIService("openai1", "key-2"))
	svc.Reconcile()
	assertShutdown(t, factory.built[1])
}

func TestCreateSessionProviderFailure(t *testing.T) {
	cfg := &fakeConfig{}
	cfg.set(enabled("openai1"), openAIService("openai1", "k"))
	svc, factory := newTestService(cfg, newLevelChecker(enterprise.LevelEnterprise), allowAll)
	factory.mintErr = errors.New("upstream 500")

	for range maxUnexpiredKeys + 1 {
		_, err := svc.CreateSession(context.Background(), testUserID)
		assert.ErrorIs(t, err, ErrProvider, "a failed mint must not hold a key slot")
	}
}

func TestRateLimiter(t *testing.T) {
	session := func(expiresIn time.Duration, now time.Time) *bifrost.TranscriptionSession {
		return &bifrost.TranscriptionSession{ExpiresAt: now.Add(expiresIn).UnixMilli()}
	}

	tests := []struct {
		name string
		run  func(t *testing.T, l *rateLimiter, clock *time.Time)
	}{
		{
			name: "unexpired keys are capped",
			run: func(t *testing.T, l *rateLimiter, clock *time.Time) {
				for range maxUnexpiredKeys {
					release, ok := l.reserve(testUserID)
					require.True(t, ok)
					release(session(time.Minute, *clock))
				}
				_, ok := l.reserve(testUserID)
				assert.False(t, ok)

				_, ok = l.reserve("othe12345678901234567890ab")
				assert.True(t, ok, "limits are per user")

				*clock = clock.Add(time.Minute + time.Second)
				_, ok = l.reserve(testUserID)
				assert.True(t, ok, "expired keys free their slot")
			},
		},
		{
			name: "mints per window are capped",
			run: func(t *testing.T, l *rateLimiter, clock *time.Time) {
				for range mintsPerWindow {
					release, ok := l.reserve(testUserID)
					require.True(t, ok)
					release(session(time.Second, *clock))
					*clock = clock.Add(2 * time.Second)
				}
				_, ok := l.reserve(testUserID)
				assert.False(t, ok)

				*clock = clock.Add(mintWindow)
				_, ok = l.reserve(testUserID)
				assert.True(t, ok)
			},
		},
		{
			name: "failed mints release their key slot",
			run: func(t *testing.T, l *rateLimiter, _ *time.Time) {
				for range maxUnexpiredKeys + 1 {
					release, ok := l.reserve(testUserID)
					require.True(t, ok)
					release(nil)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
			l := newRateLimiter()
			l.now = func() time.Time { return clock }
			tt.run(t, l, &clock)
		})
	}
}

func isShutdown(m *fakeMinter) bool {
	select {
	case <-m.shutdown:
		return true
	default:
		return false
	}
}

func assertShutdown(t *testing.T, m *fakeMinter) {
	t.Helper()
	require.Eventually(t, func() bool { return isShutdown(m) }, 5*time.Second, 5*time.Millisecond, "old minter was not shut down")
}

func assertNotShutdown(t *testing.T, m *fakeMinter) {
	t.Helper()
	require.Never(t, func() bool { return isShutdown(m) }, 50*time.Millisecond, 5*time.Millisecond, "minter was shut down while its service was unchanged")
}

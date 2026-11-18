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

func setConfig(c *config.Container, d config.DictationConfig, services ...llm.ServiceConfig) {
	c.Update(&config.Config{Services: services, Dictation: d})
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
	panics   bool
	shutdown chan struct{}
}

func (m *fakeMinter) CreateTranscriptionSession(_ context.Context, req bifrost.TranscriptionSessionRequest) (*bifrost.TranscriptionSession, error) {
	if m.panics {
		panic("provider client panicked")
	}
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
	panics  bool
}

func (f *minterFactory) build(svc llm.ServiceConfig) (sessionMinter, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := &fakeMinter{svc: svc, err: f.mintErr, panics: f.panics, shutdown: make(chan struct{})}
	f.built = append(f.built, m)
	return m, nil
}

func openAIService(id, key string) llm.ServiceConfig {
	return llm.ServiceConfig{ID: id, Name: id, Type: llm.ServiceTypeOpenAI, APIKey: key}
}

func enabled(serviceID string) config.DictationConfig {
	return config.DictationConfig{Enabled: true, ServiceID: serviceID, Model: "gpt-live-transcribe"}
}

func newTestService(cfg *config.Container, license LicenseChecker, access AccessChecker) (*Service, *minterFactory) {
	factory := &minterFactory{}
	svc := New(cfg, license, access, nopLogger{})
	svc.newMinter = factory.build
	return svc, factory
}

// The outcomes reachable through the API are covered there; these are the
// nil-dependency cases the server never wires.
func TestNilCheckersFailClosed(t *testing.T) {
	tests := []struct {
		name       string
		license    LicenseChecker
		access     AccessChecker
		wantReason string
		wantErr    error
	}{
		{name: "nil license checker", access: allowAll, wantReason: ReasonLicense, wantErr: enterprise.ErrNotLicensed},
		{name: "nil access checker", license: newLevelChecker(enterprise.LevelEnterprise), wantReason: ReasonAccessDenied, wantErr: ErrAccessDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Container{}
			setConfig(cfg, enabled("openai1"), openAIService("openai1", "k"))
			svc, factory := newTestService(cfg, tt.license, tt.access)

			avail := svc.Availability(context.Background(), testUserID)
			assert.False(t, avail.Available)
			assert.Equal(t, tt.wantReason, avail.Reason)

			session, err := svc.CreateSession(context.Background(), testUserID)
			assert.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, session)
			assert.Empty(t, factory.built, "no provider client is built for a denied request")
		})
	}
}

func TestEveryMintUsesAndShutsDownItsOwnClient(t *testing.T) {
	tests := []struct {
		name    string
		mintErr error
		panics  bool
		wantErr error
	}{
		{name: "success"},
		{name: "provider error", mintErr: errors.New("upstream 500"), wantErr: ErrProvider},
		{name: "provider panic", panics: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Container{}
			setConfig(cfg, enabled("openai1"), openAIService("openai1", "key-1"))
			svc, factory := newTestService(cfg, newLevelChecker(enterprise.LevelEnterprise), allowAll)
			factory.mintErr = tt.mintErr
			factory.panics = tt.panics

			mint := func(userID string) error {
				if tt.panics {
					assert.Panics(t, func() { _, _ = svc.CreateSession(context.Background(), userID) })
					return nil
				}
				_, err := svc.CreateSession(context.Background(), userID)
				return err
			}

			err := mint(testUserID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
			setConfig(cfg, enabled("openai1"), openAIService("openai1", "key-2"))
			_ = mint("othe12345678901234567890ab")

			require.Len(t, factory.built, 2)
			assert.Equal(t, "key-1", factory.built[0].svc.APIKey)
			assert.Equal(t, "key-2", factory.built[1].svc.APIKey, "a mint uses the config current at its checks")
			for i, m := range factory.built {
				assert.True(t, isShutdown(m), "client %d was not shut down after its mint", i)
			}
		})
	}
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
	svc := New(cfg, nil, allowAll, nopLogger{})
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

func TestCreateSessionProviderFailure(t *testing.T) {
	cfg := &config.Container{}
	setConfig(cfg, enabled("openai1"), openAIService("openai1", "k"))
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

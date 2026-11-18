// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package dictation mints short-lived browser sessions for live dictation.
// The browser streams audio straight to the provider; the server only
// decides who may dictate and holds the provider credentials.
package dictation

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// TransportWebRTC is the only browser transport in v1.
const TransportWebRTC = "webrtc"

// Reasons reported by Availability when dictation is not available.
const (
	ReasonDisabled      = "disabled"
	ReasonLicense       = "license"
	ReasonMisconfigured = "misconfigured"
	ReasonAccessDenied  = "access_denied"
)

var (
	ErrDisabled      = errors.New("dictation is disabled")
	ErrMisconfigured = errors.New("the dictation service is not configured correctly")
	ErrAccessDenied  = errors.New("you do not have access to the dictation service")
	ErrRateLimited   = errors.New("too many dictation sessions; try again shortly")
	ErrProvider      = errors.New("the transcription provider could not start a session")
)

// Config is the configuration the service reads on every call.
type Config interface {
	Config() *config.Config
}

// LicenseChecker reports whether a capability is licensed.
type LicenseChecker interface {
	Check(capability enterprise.Capability) error
}

// AccessChecker is the service-level access check agents also use.
type AccessChecker interface {
	CanUseService(ctx context.Context, userID, serviceID string) error
}

// Logger logs mint outcomes. Never pass secrets or provider responses.
type Logger interface {
	Info(message string, keyValuePairs ...any)
	Warn(message string, keyValuePairs ...any)
}

type sessionMinter interface {
	CreateTranscriptionSession(ctx context.Context, req bifrost.TranscriptionSessionRequest) (*bifrost.TranscriptionSession, error)
	Shutdown()
}

func newBifrostMinter(svc llm.ServiceConfig) (sessionMinter, error) {
	return bifrost.NewRealtimeSessionMinter(svc)
}

// Availability tells the webapp whether to show the dictation button.
type Availability struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Transport string `json:"transport"`
	Reason    string `json:"reason,omitempty"`
}

// Service decides who may dictate and mints their sessions.
//
// Each mint builds its own provider client and shuts it down afterwards
// (about 2 ms and 400 KB), so no client outlives the config and license
// checks it was built under.
type Service struct {
	config    Config
	license   LicenseChecker
	access    AccessChecker
	log       Logger
	limiter   *rateLimiter
	newMinter func(llm.ServiceConfig) (sessionMinter, error)

	// reconcileMu serializes Reconcile, so the last config or license change
	// is the one whose state is kept.
	reconcileMu sync.Mutex

	mu sync.Mutex
	// siteState is the user-independent availability last seen by
	// Reconcile; onAvailabilityChange runs when it changes.
	siteState            Availability
	onAvailabilityChange func()
}

// New builds a dictation service. A nil license or access checker denies
// every request.
func New(cfg Config, license LicenseChecker, access AccessChecker, log Logger) *Service {
	s := &Service{
		config:    cfg,
		license:   license,
		access:    access,
		log:       log,
		limiter:   newRateLimiter(),
		newMinter: newBifrostMinter,
	}
	s.siteState = availabilityFor(s.resolve())
	return s
}

// OnAvailabilityChange registers f to run when Reconcile finds that
// dictation was enabled or disabled, licensed or unlicensed, or its service
// or model changed. Per-user access changes are not detected.
func (s *Service) OnAvailabilityChange(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onAvailabilityChange = f
}

// availabilityFor reports a resolve or access check result, failing closed
// on an unexpected error.
func availabilityFor(r resolved, err error) Availability {
	avail := Availability{Enabled: r.dictation.Enabled, Transport: TransportWebRTC}
	if err != nil {
		reason, ok := reasonFor(err)
		if !ok {
			reason = ReasonMisconfigured
		}
		avail.Reason = reason
		return avail
	}
	avail.Available = true
	avail.Provider = r.service.Type
	avail.Model = r.dictation.Model
	return avail
}

// resolved is the dictation setting and its service after every check that
// does not depend on the user.
type resolved struct {
	dictation config.DictationConfig
	service   llm.ServiceConfig
}

func (s *Service) checkLicense() error {
	if s.license == nil {
		return enterprise.NewLicenseError(enterprise.CapDictation, enterprise.LevelUnlicensed)
	}
	return s.license.Check(enterprise.CapDictation)
}

// resolve fails closed: a missing service or one of the wrong type is a
// configuration error, never a reason to use another service.
func (s *Service) resolve() (resolved, error) {
	cfg := s.config.Config()
	r := resolved{dictation: cfg.Dictation}
	if !r.dictation.Enabled {
		return r, ErrDisabled
	}
	if err := s.checkLicense(); err != nil {
		return r, err
	}
	svc, err := cfg.DictationService()
	if err != nil {
		return r, fmt.Errorf("%w: %w", ErrMisconfigured, err)
	}
	r.service = svc
	return r, nil
}

func (s *Service) checkAccess(ctx context.Context, userID, serviceID string) error {
	if s.access == nil {
		return ErrAccessDenied
	}
	if err := s.access.CanUseService(ctx, userID, serviceID); err != nil {
		return fmt.Errorf("%w: %w", ErrAccessDenied, err)
	}
	return nil
}

// Availability reports whether userID can dictate right now.
func (s *Service) Availability(ctx context.Context, userID string) Availability {
	r, err := s.resolve()
	if err == nil {
		err = s.checkAccess(ctx, userID, r.service.ID)
	}
	return availabilityFor(r, err)
}

func reasonFor(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrDisabled):
		return ReasonDisabled, true
	case errors.Is(err, enterprise.ErrNotLicensed):
		return ReasonLicense, true
	case errors.Is(err, ErrMisconfigured):
		return ReasonMisconfigured, true
	case errors.Is(err, ErrAccessDenied):
		return ReasonAccessDenied, true
	default:
		return "", false
	}
}

// CreateSession mints a dictation session for userID. Every check runs on
// every call, so a license downgrade or config change applies immediately.
func (s *Service) CreateSession(ctx context.Context, userID string) (*bifrost.TranscriptionSession, error) {
	r, err := s.resolve()
	if err != nil {
		return nil, err
	}
	if err = s.checkAccess(ctx, userID, r.service.ID); err != nil {
		return nil, err
	}

	release, ok := s.limiter.reserve(userID)
	if !ok {
		return nil, ErrRateLimited
	}

	session, err := s.mint(ctx, r)
	release(session)
	switch {
	case errors.Is(err, errMinterBuild):
		s.log.Warn("Failed to build the dictation session minter", "service_id", r.service.ID, "error", err.Error())
		return nil, fmt.Errorf("%w: %w", ErrMisconfigured, err)
	case err != nil:
		s.log.Warn("Failed to mint a dictation session", "user_id", userID, "service_id", r.service.ID, "error", err.Error())
		return nil, fmt.Errorf("%w: %w", ErrProvider, err)
	}

	s.log.Info("Minted a dictation session", "user_id", userID, "service_id", r.service.ID, "model", r.dictation.Model)
	return session, nil
}

var errMinterBuild = errors.New("failed to build the session minter")

// mint shuts its client down on every exit, including a panic in the
// provider call.
func (s *Service) mint(ctx context.Context, r resolved) (*bifrost.TranscriptionSession, error) {
	minter, err := s.newMinter(r.service)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errMinterBuild, err)
	}
	defer minter.Shutdown()
	return minter.CreateTranscriptionSession(ctx, bifrost.TranscriptionSessionRequest{
		Model:    r.dictation.Model,
		Language: r.dictation.Language,
	})
}

// Reconcile reports an availability change. Call it on config and license
// changes, on every node.
func (s *Service) Reconcile() {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()

	state := availabilityFor(s.resolve())

	s.mu.Lock()
	changed := state != s.siteState
	s.siteState = state
	notify := s.onAvailabilityChange
	s.mu.Unlock()

	if changed && notify != nil {
		notify()
	}
}

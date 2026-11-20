// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package dictation brokers live dictation calls. The browser's audio goes
// straight to the provider over WebRTC; the server creates each call with
// its own key and the admin's session settings, watches it over a sideband
// connection, and ends it when it breaks policy or runs too long.
package dictation

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// TransportWebRTC is the only browser transport in v1.
const TransportWebRTC = "webrtc"

// Reasons reported by Availability when dictation is not available. A
// misconfigured service and a denied user both report ReasonUnavailable, so
// users learn nothing about the configuration or access policies.
const (
	ReasonDisabled    = "disabled"
	ReasonLicense     = "license"
	ReasonUnavailable = "unavailable"
)

// Reasons sent with a session-ended notification for calls the server ends.
const (
	EndReasonDurationLimit = "duration_limit"
	EndReasonPolicy        = "policy"
	EndReasonShutdown      = "shutdown"
)

const (
	// DefaultMaxSessionDuration is how long the server lets a call run.
	DefaultMaxSessionDuration = 10 * time.Minute
	// maxConcurrentSessions is the per-user cap across every node.
	maxConcurrentSessions = 2
	// leaseSlack lets a lease outlive its session's duration cap while the
	// hangup completes.
	leaseSlack     = time.Minute
	hangupTimeout  = 10 * time.Second
	shutdownWait   = 25 * time.Second
	callSetupLimit = 15 * time.Second
)

// hangupBackoff is the wait before each hangup attempt.
var hangupBackoff = []time.Duration{0, 250 * time.Millisecond, time.Second}

var (
	ErrDisabled         = errors.New("dictation is disabled")
	ErrMisconfigured    = errors.New("the dictation service is not configured correctly")
	ErrAccessDenied     = errors.New("you do not have access to dictation")
	ErrRateLimited      = errors.New("too many dictation sessions started; try again shortly")
	ErrConcurrentLimit  = errors.New("too many active dictation sessions")
	ErrProvider         = errors.New("the transcription provider could not start a session")
	ErrInvalidOffer     = errors.New("the request needs an SDP offer")
	ErrSessionNotFound  = errors.New("no such dictation session")
	ErrSessionsShutdown = errors.New("dictation is shutting down")
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

// UserGetter looks up the requesting user.
type UserGetter interface {
	GetUser(userID string) (*model.User, error)
}

// Logger logs session outcomes. Never pass secrets, provider responses or
// sideband event content.
type Logger interface {
	Info(message string, keyValuePairs ...any)
	Warn(message string, keyValuePairs ...any)
}

// SessionEndNotifier tells userID that the server ended sessionID.
type SessionEndNotifier func(userID, sessionID, reason string)

type callClient interface {
	CreateTranscriptionCall(ctx context.Context, offerSDP string, req bifrost.TranscriptionSessionRequest) (*bifrost.RealtimeCall, error)
	Attach(ctx context.Context, callID string, req bifrost.TranscriptionSessionRequest) (sideband, error)
	Hangup(ctx context.Context, callID string) error
}

type sideband interface {
	Run(ctx context.Context) error
	Close()
}

type bifrostCallClient struct{ *bifrost.RealtimeCallClient }

func (c bifrostCallClient) Attach(ctx context.Context, callID string, req bifrost.TranscriptionSessionRequest) (sideband, error) {
	return c.RealtimeCallClient.Attach(ctx, callID, req)
}

func newBifrostCallClient(svc llm.ServiceConfig) (callClient, error) {
	c, err := bifrost.NewRealtimeCallClient(svc)
	if err != nil {
		return nil, err
	}
	return bifrostCallClient{c}, nil
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

// Session is what the browser needs to finish connecting.
type Session struct {
	AnswerSDP          string `json:"sdp"`
	SessionID          string `json:"sessionId"`
	Provider           string `json:"provider"`
	Model              string `json:"model"`
	TurnDetection      string `json:"turnDetection"`
	MaxDurationSeconds int    `json:"maxDurationSeconds"`
}

// Service decides who may dictate, creates their calls, and polices them.
type Service struct {
	config      Config
	license     LicenseChecker
	access      AccessChecker
	users       UserGetter
	log         Logger
	limiter     *rateLimiter
	endLimiter  *rateLimiter
	leases      *sessionLeases
	channels    ChannelDirectory
	newClient   func(llm.ServiceConfig) (callClient, error)
	maxDuration time.Duration
	notifyEnd   SessionEndNotifier

	// ctx ends every watched call when the service shuts down.
	ctx      context.Context
	cancel   context.CancelFunc
	watchers sync.WaitGroup

	// reconcileMu serializes Reconcile, so the last config or license change
	// is the one whose state is kept.
	reconcileMu sync.Mutex

	mu       sync.Mutex
	shutdown bool
	// local cancels the watchers of calls this node started, by session.
	local map[string]context.CancelFunc
	// ending coalesces concurrent ends of the same session.
	ending map[string]*endFlight
	// siteState is the user-independent availability last seen by
	// Reconcile; onAvailabilityChange runs when it changes.
	siteState            Availability
	onAvailabilityChange func()
}

// Option adjusts a Service.
type Option func(*Service)

// WithMaxSessionDuration overrides DefaultMaxSessionDuration.
func WithMaxSessionDuration(d time.Duration) Option {
	return func(s *Service) { s.maxDuration = d }
}

// WithChannelDirectory sets where channel context is read from. Without
// one, sessions get no channel context.
func WithChannelDirectory(d ChannelDirectory) Option {
	return func(s *Service) { s.channels = d }
}

// WithSessionEndNotifier sets the notifier for calls the server ends.
func WithSessionEndNotifier(n SessionEndNotifier) Option {
	return func(s *Service) { s.notifyEnd = n }
}

// New builds a dictation service. A nil license checker, access checker,
// user getter or lease store denies every request.
func New(cfg Config, license LicenseChecker, access AccessChecker, users UserGetter, leases LeaseStore, log Logger, opts ...Option) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		config:      cfg,
		license:     license,
		access:      access,
		users:       users,
		log:         log,
		limiter:     newRateLimiter(sessionStartsPerWindow, sessionStartWindow),
		endLimiter:  newRateLimiter(sessionEndsPerWindow, sessionStartWindow),
		newClient:   newBifrostCallClient,
		maxDuration: DefaultMaxSessionDuration,
		ctx:         ctx,
		cancel:      cancel,
		local:       map[string]context.CancelFunc{},
		ending:      map[string]*endFlight{},
	}
	for _, opt := range opts {
		opt(s)
	}
	if leases != nil {
		s.leases = &sessionLeases{store: leases, max: maxConcurrentSessions, leaseTTL: s.maxDuration + leaseSlack, now: time.Now}
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
	switch {
	case err == nil:
		avail.Available = true
		avail.Provider = r.service.Type
		avail.Model = r.dictation.Model
	case errors.Is(err, ErrDisabled):
		avail.Reason = ReasonDisabled
	case errors.Is(err, enterprise.ErrNotLicensed):
		avail.Reason = ReasonLicense
	default:
		avail.Reason = ReasonUnavailable
	}
	return avail
}

// resolved is the dictation setting and its service after every check that
// does not depend on the user.
type resolved struct {
	dictation config.DictationConfig
	service   llm.ServiceConfig
}

func (r resolved) request() bifrost.TranscriptionSessionRequest {
	return bifrost.TranscriptionSessionRequest{Model: r.dictation.Model, Language: r.dictation.Language}
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

// checkUser denies guests, bots and deactivated accounts. A failed lookup
// denies too.
func (s *Service) checkUser(userID string) error {
	if s.users == nil {
		return ErrAccessDenied
	}
	user, err := s.users.GetUser(userID)
	if err != nil || user == nil {
		return fmt.Errorf("%w: user lookup failed", ErrAccessDenied)
	}
	if user.IsGuest() || user.IsBot || user.DeleteAt != 0 {
		return ErrAccessDenied
	}
	return nil
}

func (s *Service) checkAccess(ctx context.Context, userID, serviceID string) error {
	if err := s.checkUser(userID); err != nil {
		return err
	}
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

// CreateSession creates a call for userID from the browser's SDP offer.
// Every check runs on every call, so a license downgrade or config change
// applies immediately. The sideband is attached before the answer is
// returned, so the browser cannot change the session unobserved.
//
// channelID, when set, is the channel the user dictates into. With channel
// context on, a user who can't read it is denied; if its context can't be
// read, the session gets none.
func (s *Service) CreateSession(ctx context.Context, userID, offerSDP, channelID string) (*Session, error) {
	if offerSDP == "" || len(offerSDP) > bifrost.MaxSDPBytes {
		return nil, ErrInvalidOffer
	}
	r, err := s.resolve()
	if err != nil {
		return nil, err
	}
	if err = s.checkAccess(ctx, userID, r.service.ID); err != nil {
		return nil, err
	}
	// The access check can take a while; a config or license change during
	// it must not let the request use what it read before.
	if current, recheckErr := s.resolve(); recheckErr != nil {
		return nil, recheckErr
	} else if !reflect.DeepEqual(current, r) {
		return nil, fmt.Errorf("%w: the configuration changed during the request", ErrMisconfigured)
	}
	audit.AddParam(audit.RecordFromContext(ctx), audit.KeyServiceID, r.service.ID)

	if !s.limiter.allow(userID) {
		return nil, ErrRateLimited
	}
	req := r.request()
	if channelID != "" && r.dictation.ChannelContextEnabled() && bifrost.SupportsTranscriptionContext(req.Model) {
		hints, hintsErr := s.channelContext(userID, channelID)
		switch {
		case errors.Is(hintsErr, ErrAccessDenied):
			return nil, hintsErr
		case hintsErr != nil:
			s.log.Warn("Failed to read dictation channel context; sending none", "user_id", userID, "channel_id", channelID, "error", hintsErr.Error())
		default:
			req.Prompt, req.Keywords = hints.Prompt, hints.Keywords
		}
	}
	if s.leases == nil {
		return nil, fmt.Errorf("%w: no session store", ErrMisconfigured)
	}

	sessionID := model.NewId()
	if err = s.leases.acquire(userID, sessionID); err != nil {
		if errors.Is(err, errConcurrentLimit) {
			return nil, ErrConcurrentLimit
		}
		s.log.Warn("Failed to reserve a dictation session", "user_id", userID, "error", err.Error())
		return nil, fmt.Errorf("%w: %w", ErrMisconfigured, err)
	}

	// Shutdown waits for starts in progress, so it never misses a call
	// created after it began.
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		s.releaseLease(userID, sessionID)
		return nil, ErrSessionsShutdown
	}
	s.watchers.Add(1)
	s.mu.Unlock()
	defer s.watchers.Done()

	session, err := s.startCall(ctx, userID, sessionID, r, req, offerSDP)
	if err == nil {
		audit.AddParam(audit.RecordFromContext(ctx), audit.KeyDictationSessionID, sessionID)
	}
	return session, err
}

// startCall creates and attaches the call. On failure it frees the
// session's slot, unless a created call could not be hung up.
func (s *Service) startCall(ctx context.Context, userID, sessionID string, r resolved, req bifrost.TranscriptionSessionRequest, offerSDP string) (*Session, error) {
	client, err := s.newClient(r.service)
	if err != nil {
		s.releaseLease(userID, sessionID)
		s.log.Warn("Failed to build the dictation call client", "service_id", r.service.ID, "error", err.Error())
		return nil, fmt.Errorf("%w: %w", ErrMisconfigured, err)
	}

	setupCtx, cancel := context.WithTimeout(ctx, callSetupLimit)
	defer cancel()
	call, err := client.CreateTranscriptionCall(setupCtx, offerSDP, req)
	if err != nil {
		s.log.Warn("Failed to create a dictation call", "user_id", userID, "service_id", r.service.ID, "error", err.Error())
		if created, ok := errors.AsType[*bifrost.CreatedCallError](err); ok {
			s.endCall(client, created.CallID, userID, sessionID)
		} else {
			s.releaseLease(userID, sessionID)
		}
		return nil, fmt.Errorf("%w: %w", ErrProvider, err)
	}

	// From here a call exists on the provider: every failure hangs it up.
	sb, err := client.Attach(setupCtx, call.CallID, req)
	if err != nil {
		s.log.Warn("Failed to attach to a dictation call; hanging up", "user_id", userID, "service_id", r.service.ID, "error", err.Error())
		s.endCall(client, call.CallID, userID, sessionID)
		return nil, fmt.Errorf("%w: %w", ErrProvider, err)
	}

	if err = s.leases.setCall(userID, sessionID, call.CallID, r.service.ID); err != nil {
		s.log.Warn("Failed to record a dictation call; hanging up", "user_id", userID, "session_id", sessionID, "error", err.Error())
		sb.Close()
		s.endCall(client, call.CallID, userID, sessionID)
		return nil, fmt.Errorf("%w: %w", ErrMisconfigured, err)
	}

	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		sb.Close()
		s.endCall(client, call.CallID, userID, sessionID)
		return nil, ErrSessionsShutdown
	}
	s.watchers.Add(1)
	watchCtx, stopWatch := context.WithCancel(s.ctx)
	s.local[sessionID] = stopWatch
	s.mu.Unlock()
	go s.watch(watchCtx, client, sb, call.CallID, userID, sessionID)

	s.log.Info("Started a dictation session", "user_id", userID, "service_id", r.service.ID, "session_id", sessionID, "model", r.dictation.Model)
	return &Session{
		AnswerSDP:          call.AnswerSDP,
		SessionID:          sessionID,
		Provider:           r.service.Type,
		Model:              r.dictation.Model,
		TurnDetection:      call.TurnDetection,
		MaxDurationSeconds: int(s.maxDuration / time.Second),
	}, nil
}

// watch polices one call until its sideband stops, then hangs it up. A
// dropped sideband doesn't prove the call ended, so the call is always hung
// up before its slot is freed.
func (s *Service) watch(ctx context.Context, client callClient, sb sideband, callID, userID, sessionID string) {
	defer s.watchers.Done()
	defer func() {
		s.mu.Lock()
		if stop, ok := s.local[sessionID]; ok {
			stop()
			delete(s.local, sessionID)
		}
		s.mu.Unlock()
	}()

	runCtx, cancel := context.WithTimeout(ctx, s.maxDuration)
	defer cancel()
	err := sb.Run(runCtx)

	reason := ""
	switch {
	case errors.Is(err, bifrost.ErrSessionChanged):
		reason = EndReasonPolicy
	case s.ctx.Err() != nil:
		reason = EndReasonShutdown
	case ctx.Err() != nil:
		// EndSession already hung up the call.
		s.releaseLease(userID, sessionID)
		return
	case errors.Is(err, context.DeadlineExceeded):
		reason = EndReasonDurationLimit
	}
	s.log.Info("Ending a dictation session", "user_id", userID, "session_id", sessionID, "reason", cmp.Or(reason, "sideband closed"))
	s.endCall(client, callID, userID, sessionID)
	if reason != "" && s.notifyEnd != nil {
		s.notifyEnd(userID, sessionID, reason)
	}
}

// endCall hangs up the call and frees its slot. If the hangup fails, the
// slot stays taken until its lease expires, since the call may still run.
func (s *Service) endCall(client callClient, callID, userID, sessionID string) {
	if err := s.hangup(client, callID); err != nil {
		s.log.Warn("Failed to hang up a dictation call; its slot stays taken until it expires", "user_id", userID, "session_id", sessionID, "error", err.Error())
		return
	}
	s.releaseLease(userID, sessionID)
}

// hangup ends callID, retrying failures that may be transient.
func (s *Service) hangup(client callClient, callID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), hangupTimeout)
	defer cancel()
	var err error
	for attempt, backoff := range hangupBackoff {
		if attempt > 0 {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return errors.Join(err, ctx.Err())
			}
		}
		if err = client.Hangup(ctx, callID); err == nil || !bifrost.IsTransientRealtimeError(err) {
			return err
		}
	}
	return err
}

func (s *Service) releaseLease(userID, sessionID string) {
	if err := s.leases.release(userID, sessionID); err != nil {
		s.log.Warn("Failed to release a dictation session; it expires on its own", "user_id", userID, "session_id", sessionID, "error", err.Error())
	}
}

type endFlight struct {
	done chan struct{}
	err  error
}

// EndSession hangs up userID's session and frees its slot. Any node can end
// any session, since the call is recorded in shared storage. Ending a session
// that already ended succeeds; a session that isn't userID's is
// ErrSessionNotFound. It works whatever the current license or dictation
// setting, so a call can always be ended. If the hangup fails, the slot
// stays taken and ErrProvider is returned. Concurrent ends of one session
// share a single hangup, and ends are rate limited per user.
func (s *Service) EndSession(ctx context.Context, userID, sessionID string) error {
	if s.leases == nil || !model.IsValidId(sessionID) {
		return ErrSessionNotFound
	}
	if !s.endLimiter.allow(userID) {
		return ErrRateLimited
	}

	key := userID + "/" + sessionID
	s.mu.Lock()
	if flight, ok := s.ending[key]; ok {
		s.mu.Unlock()
		select {
		case <-flight.done:
			return flight.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	flight := &endFlight{done: make(chan struct{})}
	s.ending[key] = flight
	s.mu.Unlock()

	flight.err = s.endSession(ctx, userID, sessionID)
	s.mu.Lock()
	delete(s.ending, key)
	s.mu.Unlock()
	close(flight.done)
	return flight.err
}

func (s *Service) endSession(ctx context.Context, userID, sessionID string) error {
	l, err := s.leases.get(userID, sessionID)
	if errors.Is(err, errLeaseNotFound) {
		return ErrSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to end dictation session: %w", err)
	}
	if l.ServiceID != "" {
		audit.AddParam(audit.RecordFromContext(ctx), audit.KeyServiceID, l.ServiceID)
	}
	if l.Ended {
		return nil
	}

	// Without a call yet the start is still in progress; marking the
	// session ended makes that start hang up its call.
	if l.CallID != "" {
		svc, ok := s.config.Config().GetServiceByID(l.ServiceID)
		if !ok {
			s.log.Warn("Can't hang up a dictation call: its service is no longer configured", "user_id", userID, "session_id", sessionID)
			return ErrProvider
		}
		client, clientErr := s.newClient(svc)
		if clientErr != nil {
			s.log.Warn("Can't hang up a dictation call", "user_id", userID, "session_id", sessionID, "error", clientErr.Error())
			return ErrProvider
		}
		if err = s.hangup(client, l.CallID); err != nil {
			s.log.Warn("Failed to hang up a dictation call", "user_id", userID, "session_id", sessionID, "error", err.Error())
			return ErrProvider
		}
	}
	s.releaseLease(userID, sessionID)

	// Stop this node's watcher now, in case the provider leaves its
	// sideband open after the hangup.
	s.mu.Lock()
	if stop, ok := s.local[sessionID]; ok {
		stop()
		delete(s.local, sessionID)
	}
	s.mu.Unlock()
	s.log.Info("Dictation session ended by its user", "user_id", userID, "session_id", sessionID)
	return nil
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

// Shutdown hangs up every call this node watches and waits, up to a bound,
// for them to end. Call it on plugin deactivation.
func (s *Service) Shutdown() {
	s.mu.Lock()
	s.shutdown = true
	s.mu.Unlock()
	s.cancel()

	done := make(chan struct{})
	go func() {
		s.watchers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownWait):
		s.log.Warn("Timed out hanging up dictation sessions on shutdown")
	}
}

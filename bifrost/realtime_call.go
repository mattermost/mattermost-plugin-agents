// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	bifrostnet "github.com/maximhq/bifrost/core/network"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// Realtime calls use OpenAI's unified WebRTC interface: the server posts the
// browser's SDP offer with the session configuration and the service key,
// attaches a sideband WebSocket to the call, and hangs it up when needed.
// Bifrost's own SDP exchange drops the call ID (the Location header) that
// the sideband and hangup need, so these requests are made here directly.
const (
	defaultOpenAIBaseURL = "https://api.openai.com"
	realtimeCallsPath    = "/v1/realtime/calls"
	realtimeSidebandPath = "/v1/realtime"

	realtimeRequestTimeout = 15 * time.Second
	// MaxSDPBytes caps an SDP offer or answer. Browser offers are a few KiB.
	MaxSDPBytes = 64 * 1024
	// realtimeEventMaxBytes caps one sideband message.
	realtimeEventMaxBytes = 1 << 20
	realtimeErrorMaxBytes = 16 * 1024
	// sidebandPingInterval keeps proxies from closing an idle sideband
	// while the browser's call is still up; a quiet session sends nothing.
	sidebandPingInterval = 20 * time.Second
)

// Turn detection modes reported to the browser. With TurnDetectionNone the
// client commits each audio segment itself.
const (
	TurnDetectionNone      = "none"
	TurnDetectionServerVAD = "server_vad"
)

// clientTurnDetectionModels need turn_detection: null, matched by prefix so
// dated snapshots of the same model behave the same.
var clientTurnDetectionModels = []string{"gpt-live-transcribe", "gpt-realtime-whisper"}

// TurnDetectionForModel returns the turn detection mode a transcription
// session uses for model.
func TurnDetectionForModel(model string) string {
	for _, prefix := range clientTurnDetectionModels {
		if strings.HasPrefix(model, prefix) {
			return TurnDetectionNone
		}
	}
	return TurnDetectionServerVAD
}

// languageListModels take the language hint as a languages list instead of
// language; OpenAI says not to send both. Matched by prefix, like
// clientTurnDetectionModels.
var languageListModels = []string{"gpt-live-transcribe", "gpt-transcribe"}

func usesLanguageList(model string) bool {
	for _, prefix := range languageListModels {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

// SupportsTranscriptionContext reports whether model takes a prompt and
// keywords. The same models take a languages list.
func SupportsTranscriptionContext(model string) bool {
	return usesLanguageList(model)
}

// TranscriptionSessionRequest configures a transcription-only realtime session.
type TranscriptionSessionRequest struct {
	Model string
	// Language is an optional ISO-639-1 hint.
	Language string
	// Prompt and Keywords are context hints, sent only to models that
	// SupportsTranscriptionContext. The sideband accepts exactly these.
	Prompt   string
	Keywords []string
}

// RealtimeCall is a created call. AnswerSDP goes to the browser; CallID
// stays on the server.
type RealtimeCall struct {
	CallID        string
	AnswerSDP     string
	TurnDetection string
}

// ErrSessionChanged is returned by Sideband.Run when the call's session
// no longer matches the configuration it was created with.
var ErrSessionChanged = errors.New("the realtime session was changed away from its configuration")

// ErrSidebandClosed is returned by Sideband.Run when the provider ends the
// call or the connection drops.
var ErrSidebandClosed = errors.New("the realtime sideband connection closed")

// CreatedCallError is returned when the provider created a call but the
// response was unusable. The call is still running: hang up CallID.
type CreatedCallError struct {
	CallID string
	Err    error
}

func (e *CreatedCallError) Error() string { return e.Err.Error() }
func (e *CreatedCallError) Unwrap() error { return e.Err }

var callIDPattern = regexp.MustCompile(`^rtc_[A-Za-z0-9_-]{1,128}$`)

// RealtimeCallClient creates, watches and hangs up realtime calls for one
// service with the service's credentials.
type RealtimeCallClient struct {
	baseURL     *url.URL
	apiKey      string
	orgID       string
	serviceType string
	httpClient  *http.Client
	dialer      *websocket.Dialer
}

// NewRealtimeCallClient builds a client for service, which must be one of
// RealtimeSessionServiceTypes and have an absolute http(s) API URL, if any.
func NewRealtimeCallClient(service llm.ServiceConfig) (*RealtimeCallClient, error) {
	provider, err := ProviderForServiceType(service.Type, RealtimeSessionServiceTypes)
	if err != nil {
		return nil, err
	}
	if service.APIKey == "" {
		return nil, errors.New("the service has no API key")
	}
	base := providerSettingsFromService(provider, service).APIURL
	if strings.TrimSpace(base) == "" {
		base = defaultOpenAIBaseURL
	}
	baseURL, err := ParseRealtimeBaseURL(base)
	if err != nil {
		return nil, err
	}

	return &RealtimeCallClient{
		baseURL:     baseURL,
		apiKey:      service.APIKey,
		orgID:       service.OrgID,
		serviceType: service.Type,
		httpClient: &http.Client{
			Timeout:   realtimeRequestTimeout,
			Transport: realtimeTransport,
			// The key is sent to the configured origin only.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		dialer: realtimeWSDialer,
	}, nil
}

// realtimeTransport and realtimeWSDialer are shared by every call client, so
// per-call clients don't each hold idle connections. Both connect through
// realtimeNetDialer.
var (
	realtimeNetDialer = newGuardedDialer()
	realtimeTransport = newRealtimeTransport()
	realtimeWSDialer  = &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: realtimeRequestTimeout,
		NetDialContext:   realtimeNetDialer.DialContext,
	}
)

func newRealtimeTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = realtimeNetDialer.DialContext
	return transport
}

// newGuardedDialer returns a dialer that refuses link-local, cloud metadata,
// multicast and unspecified addresses. The check runs
// on each address the dialer actually connects to, after resolution, so
// fallback across a host's addresses still works, every address tried is
// checked, and a second resolution can't swap one in. Private and loopback
// addresses stay allowed, as for chat requests: service URLs often point at
// self-hosted gateways.
func newGuardedDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   realtimeRequestTimeout,
		KeepAlive: 30 * time.Second,
		Control:   guardConnect,
	}
}

func guardConnect(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("refusing to connect to unresolved address %q", address)
	}
	return checkDialableIP(ip)
}

// checkDialableIP applies Bifrost's private-network policy (unspecified,
// link-local and cloud metadata endpoints, including IPv4 embedded in IPv6)
// and refuses multicast.
func checkDialableIP(ip net.IP) error {
	if ip.IsMulticast() {
		return fmt.Errorf("blocked connection to %s", ip)
	}
	if err := bifrostnet.CheckPrivateNetworkLiteral(ip.String()); err != nil {
		return fmt.Errorf("blocked connection to %s: %w", ip, err)
	}
	return nil
}

// checkTarget applies the dial guard to the service host itself when the
// request goes through a proxy, which then dials it instead of this server.
// Since the proxy picks the address, every address must pass.
func (c *RealtimeCallClient) checkTarget(ctx context.Context) error {
	proxyURL, err := http.ProxyFromEnvironment(&http.Request{URL: c.baseURL})
	if err != nil {
		return fmt.Errorf("invalid proxy settings: %w", err)
	}
	if proxyURL == nil {
		return nil
	}
	host := c.baseURL.Hostname()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("DNS lookup failed for %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("DNS lookup for %s returned no addresses", host)
	}
	for _, ip := range ips {
		if err = checkDialableIP(ip); err != nil {
			return err
		}
	}
	return nil
}

// ParseRealtimeBaseURL accepts only an absolute http or https URL with a
// host and path, and no credentials, query or fragment.
func ParseRealtimeBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("the service API URL must be an absolute http or https URL with only a host and path")
	}
	return u, nil
}

func (c *RealtimeCallClient) endpoint(path string) string {
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String()
}

func (c *RealtimeCallClient) setAuth(h http.Header) {
	h.Set("Authorization", "Bearer "+c.apiKey)
	if c.orgID != "" {
		h.Set("OpenAI-Organization", c.orgID)
	}
}

type realtimeTranscriptionSession struct {
	Type  string        `json:"type"`
	Audio realtimeAudio `json:"audio"`
}

type realtimeAudio struct {
	Input realtimeAudioInput `json:"input"`
}

type realtimeAudioInput struct {
	Transcription  realtimeTranscription  `json:"transcription"`
	NoiseReduction realtimeNoiseReduction `json:"noise_reduction"`
	// TurnDetection is sent as null when the client segments audio itself.
	TurnDetection *realtimeTurnDetection `json:"turn_detection"`
}

type realtimeTranscription struct {
	Model     string   `json:"model"`
	Language  string   `json:"language,omitempty"`
	Languages []string `json:"languages,omitempty"`
	Prompt    string   `json:"prompt,omitempty"`
	Keywords  []string `json:"keywords,omitempty"`
}

type realtimeNoiseReduction struct {
	Type string `json:"type"`
}

type realtimeTurnDetection struct {
	Type string `json:"type"`
}

func transcriptionSettings(req TranscriptionSessionRequest) realtimeTranscription {
	t := realtimeTranscription{Model: req.Model}
	switch {
	case req.Language == "":
	case usesLanguageList(req.Model):
		t.Languages = []string{req.Language}
	default:
		t.Language = req.Language
	}
	if SupportsTranscriptionContext(req.Model) {
		t.Prompt = req.Prompt
		t.Keywords = req.Keywords
	}
	return t
}

func buildTranscriptionSession(req TranscriptionSessionRequest) realtimeTranscriptionSession {
	input := realtimeAudioInput{
		Transcription:  transcriptionSettings(req),
		NoiseReduction: realtimeNoiseReduction{Type: "near_field"},
	}
	if TurnDetectionForModel(req.Model) == TurnDetectionServerVAD {
		input.TurnDetection = &realtimeTurnDetection{Type: TurnDetectionServerVAD}
	}
	return realtimeTranscriptionSession{Type: "transcription", Audio: realtimeAudio{Input: input}}
}

// providerCallError describes a failed provider request for the server log
// only; it carries the provider's error message, never the raw body.
type providerCallError struct {
	op      string
	status  int
	errType string
	code    string
	message string
}

func (e *providerCallError) Error() string {
	parts := []string{fmt.Sprintf("status=%d", e.status)}
	if e.errType != "" {
		parts = append(parts, "type="+e.errType)
	}
	if e.code != "" {
		parts = append(parts, "code="+e.code)
	}
	msg := e.op + " failed (" + strings.Join(parts, " ") + ")"
	if e.message != "" {
		msg += ": " + e.message
	}
	return msg
}

func (c *RealtimeCallClient) readProviderError(op string, resp *http.Response) error {
	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, realtimeErrorMaxBytes))
	callErr := &providerCallError{op: op, status: resp.StatusCode}
	if json.Unmarshal(raw, &body) == nil {
		callErr.errType = body.Error.Type
		callErr.code = body.Error.Code
		callErr.message = llm.SanitizeProviderErrorMessage(body.Error.Message, c.apiKey)
	}
	return callErr
}

// recordCallError puts only structured fields on span: provider error text
// can carry request content.
func recordCallError(span trace.Span, err error) {
	if callErr, ok := errors.AsType[*providerCallError](err); ok {
		attrs := []attribute.KeyValue{telemetry.LLMBifrostStatusCode.Int(callErr.status)}
		if callErr.errType != "" {
			attrs = append(attrs, telemetry.LLMBifrostErrorType.String(callErr.errType))
		}
		if callErr.code != "" {
			attrs = append(attrs, telemetry.LLMBifrostErrorCode.String(callErr.code))
		}
		span.SetAttributes(attrs...)
	}
	span.SetStatus(codes.Error, "realtime call request failed")
}

// CreateTranscriptionCall exchanges offerSDP for an answer on a new
// transcription call. The session is built here; nothing comes from the
// browser but the offer.
func (c *RealtimeCallClient) CreateTranscriptionCall(ctx context.Context, offerSDP string, req TranscriptionSessionRequest) (call *RealtimeCall, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "dictation.create_call", trace.WithAttributes(
		telemetry.LLMProvider.String(c.serviceType),
		telemetry.LLMModel.String(req.Model),
	))
	defer span.End()
	defer func() {
		if err != nil {
			recordCallError(span, err)
		}
	}()

	if strings.TrimSpace(req.Model) == "" {
		return nil, errors.New("a transcription model is required")
	}
	if strings.TrimSpace(offerSDP) == "" || len(offerSDP) > MaxSDPBytes {
		return nil, errors.New("the SDP offer is empty or too large")
	}
	if err = c.checkTarget(ctx); err != nil {
		return nil, err
	}
	session, err := json.Marshal(buildTranscriptionSession(req))
	if err != nil {
		return nil, fmt.Errorf("failed to encode the realtime session: %w", err)
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err = form.WriteField("sdp", offerSDP); err != nil {
		return nil, fmt.Errorf("failed to encode the call request: %w", err)
	}
	if err = form.WriteField("session", string(session)); err != nil {
		return nil, fmt.Errorf("failed to encode the call request: %w", err)
	}
	if err = form.Close(); err != nil {
		return nil, fmt.Errorf("failed to encode the call request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(realtimeCallsPath), &body)
	if err != nil {
		return nil, fmt.Errorf("failed to build the call request: %w", err)
	}
	httpReq.Header.Set("Content-Type", form.FormDataContentType())
	c.setAuth(httpReq.Header)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("realtime call request failed: %w", llm.SanitizeProviderError(err, c.apiKey))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, c.readProviderError("realtime call creation", resp)
	}

	callID := callIDFromLocation(resp.Header.Get("Location"))
	if callID == "" {
		return nil, errors.New("the realtime call response has no valid call ID")
	}
	// The call exists from here: the caller must hang it up on failure.
	answer, err := io.ReadAll(io.LimitReader(resp.Body, MaxSDPBytes+1))
	if err == nil && (len(answer) > MaxSDPBytes || !bytes.HasPrefix(answer, []byte("v="))) {
		err = errors.New("the realtime call response is not an SDP answer")
	}
	if err != nil {
		return nil, &CreatedCallError{CallID: callID, Err: fmt.Errorf("failed to read the SDP answer: %w", err)}
	}
	return &RealtimeCall{CallID: callID, AnswerSDP: string(answer), TurnDetection: TurnDetectionForModel(req.Model)}, nil
}

func callIDFromLocation(location string) string {
	if location == "" {
		return ""
	}
	u, err := url.Parse(location)
	if err != nil {
		return ""
	}
	id := u.Path[strings.LastIndex(u.Path, "/")+1:]
	if !callIDPattern.MatchString(id) {
		return ""
	}
	return id
}

// Hangup ends the call for every participant. A call that is already gone
// (404) counts as ended.
func (c *RealtimeCallClient) Hangup(ctx context.Context, callID string) (err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "dictation.hangup_call")
	defer span.End()
	defer func() {
		if err != nil {
			recordCallError(span, err)
		}
	}()

	if !callIDPattern.MatchString(callID) {
		return errors.New("invalid call ID")
	}
	if err = c.checkTarget(ctx); err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(realtimeCallsPath+"/"+callID+"/hangup"), nil)
	if err != nil {
		return fmt.Errorf("failed to build the hangup request: %w", err)
	}
	c.setAuth(httpReq.Header)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("realtime hangup request failed: %w", llm.SanitizeProviderError(err, c.apiKey))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil // already ended
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.readProviderError("realtime hangup", resp)
	}
	return nil
}

// IsTransientRealtimeError reports whether a failed realtime request may
// succeed on retry: a transport error, a timeout, a 429 or a 5xx.
func IsTransientRealtimeError(err error) bool {
	if err == nil {
		return false
	}
	if callErr, ok := errors.AsType[*providerCallError](err); ok {
		return callErr.status == http.StatusTooManyRequests || callErr.status >= 500
	}
	return true
}

// Sideband is the server's own connection to a call. It sees every event
// of the session, including transcripts: never log or store event content.
type Sideband struct {
	conn         *websocket.Conn
	expected     TranscriptionSessionRequest
	pingInterval time.Duration
	// pongGrace is how long past two ping intervals Run waits for a pong
	// or event before treating the connection as dead.
	pongGrace time.Duration
}

// Attach opens the sideband connection to callID, which must have been
// created with req.
func (c *RealtimeCallClient) Attach(ctx context.Context, callID string, req TranscriptionSessionRequest) (sb *Sideband, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "dictation.attach_sideband")
	defer span.End()
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, "sideband attach failed")
		}
	}()

	if !callIDPattern.MatchString(callID) {
		return nil, errors.New("invalid call ID")
	}
	if err = c.checkTarget(ctx); err != nil {
		return nil, err
	}
	u := *c.baseURL
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + realtimeSidebandPath
	u.RawQuery = url.Values{"call_id": {callID}}.Encode()

	header := http.Header{}
	c.setAuth(header)
	conn, resp, err := c.dialer.DialContext(ctx, u.String(), header)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
			span.SetAttributes(telemetry.LLMBifrostStatusCode.Int(status))
		}
		return nil, fmt.Errorf("sideband attach failed (status=%d): %w", status, llm.SanitizeProviderError(err, c.apiKey))
	}
	conn.SetReadLimit(realtimeEventMaxBytes)
	return &Sideband{conn: conn, expected: req, pingInterval: sidebandPingInterval, pongGrace: realtimeRequestTimeout}, nil
}

// Run reads the call's events until ctx ends, the call ends
// (ErrSidebandClosed), or the session is changed away from its
// configuration (ErrSessionChanged). It closes the connection on return.
func (s *Sideband) Run(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { _ = s.conn.Close() })
	defer stop()
	defer s.conn.Close()

	// A connection that drops without a close (a half-open TCP connection)
	// stops answering pings; the read deadline then ends Run.
	readTimeout := 2*s.pingInterval + s.pongGrace
	extend := func() { _ = s.conn.SetReadDeadline(time.Now().Add(readTimeout)) }
	extend()
	s.conn.SetPongHandler(func(string) error { extend(); return nil })

	pingCtx, stopPings := context.WithCancel(ctx)
	defer stopPings()
	go s.ping(pingCtx)

	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// An event too large to read can't be checked, so it fails
			// closed like a policy change.
			if errors.Is(err, websocket.ErrReadLimit) {
				return ErrSessionChanged
			}
			return ErrSidebandClosed
		}
		extend()
		if !sessionEventMatches(data, s.expected) {
			return ErrSessionChanged
		}
	}
}

// ping sends a WebSocket ping every pingInterval until ctx ends. A failed
// ping closes the connection, which ends Run. WriteControl is safe alongside
// the reader.
func (s *Sideband) ping(ctx context.Context) {
	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(realtimeRequestTimeout)); err != nil {
				_ = s.conn.Close()
				return
			}
		}
	}
}

// Close closes the sideband connection without ending the call.
func (s *Sideband) Close() {
	_ = s.conn.Close()
}

type sessionEvent struct {
	Type    string          `json:"type"`
	Session json.RawMessage `json:"session"`
}

type observedSession struct {
	Type  string `json:"type"`
	Audio struct {
		Input struct {
			Transcription *struct {
				Model     string   `json:"model"`
				Language  *string  `json:"language"`
				Languages []string `json:"languages"`
				Prompt    *string  `json:"prompt"`
				Keywords  []string `json:"keywords"`
			} `json:"transcription"`
			TurnDetection *struct {
				Type string `json:"type"`
			} `json:"turn_detection"`
		} `json:"input"`
	} `json:"audio"`
}

// sessionEventMatches reports whether a sideband event leaves the session as
// configured. Events other than session reports are not inspected.
func sessionEventMatches(data []byte, expected TranscriptionSessionRequest) bool {
	var ev sessionEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return false
	}
	switch {
	case strings.HasPrefix(ev.Type, "response."):
		// A transcription session never generates responses.
		return false
	case ev.Type == "session.created", ev.Type == "session.updated", ev.Type == "transcription_session.created", ev.Type == "transcription_session.updated":
	default:
		return true
	}

	var got observedSession
	if err := json.Unmarshal(ev.Session, &got); err != nil {
		return false
	}
	tr := got.Audio.Input.Transcription
	if got.Type != "transcription" || tr == nil || tr.Model != expected.Model {
		return false
	}
	// Only the server-built context is allowed, unchanged.
	want := transcriptionSettings(expected)
	if tr.Prompt != nil && *tr.Prompt != want.Prompt || tr.Prompt == nil && want.Prompt != "" {
		return false
	}
	if !slices.Equal(tr.Keywords, want.Keywords) && (len(tr.Keywords) > 0 || len(want.Keywords) > 0) {
		return false
	}
	// OpenAI may report language as languages, or the reverse.
	var gotLanguages []string
	if tr.Language != nil && *tr.Language != "" {
		gotLanguages = append(gotLanguages, *tr.Language)
	}
	for _, l := range tr.Languages {
		if !slices.Contains(gotLanguages, l) {
			gotLanguages = append(gotLanguages, l)
		}
	}
	var wantLanguages []string
	if expected.Language != "" {
		wantLanguages = []string{expected.Language}
	}
	if !slices.Equal(gotLanguages, wantLanguages) {
		return false
	}
	gotTurn := TurnDetectionNone
	if td := got.Audio.Input.TurnDetection; td != nil {
		gotTurn = td.Type
	}
	return gotTurn == TurnDetectionForModel(expected.Model)
}

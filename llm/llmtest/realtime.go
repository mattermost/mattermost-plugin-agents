// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// FakeAnswerSDP is the SDP answer FakeRealtime returns.
const FakeAnswerSDP = "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n"

// FakeRealtime is an OpenAI Realtime calls API for tests: SDP exchange on
// POST /v1/realtime/calls, the sideband WebSocket on /v1/realtime?call_id=,
// and POST /v1/realtime/calls/{id}/hangup.
type FakeRealtime struct {
	Server *httptest.Server

	mu       sync.Mutex
	nextID   int
	sessions []json.RawMessage
	offers   []string
	auth     []string
	hangups  []string
	// hangupAttempts includes failed hangups.
	hangupAttempts []string
	conns          map[string]*websocket.Conn
	pings          map[string]int
	closed         map[string]bool
	attached       map[string]chan struct{}

	// CallStatus, when set, fails call creation with that status and an
	// error body containing CallErrorMessage.
	CallStatus       int
	CallErrorMessage string
	// AttachStatus, when set, refuses the sideband with that status.
	AttachStatus int
	// OmitLocation drops the Location header from call creation.
	OmitLocation bool
	// AnswerBody, when set, replaces the SDP answer.
	AnswerBody string
	// CallDelay delays call creation.
	CallDelay time.Duration
	// HangupFailures fails that many hangups with a 503 before succeeding.
	HangupFailures int
	// HangupStatus, when set, answers every hangup with that status.
	HangupStatus int
	// HangupDelay delays every hangup.
	HangupDelay time.Duration
	// KeepSidebandOnHangup leaves the sideband open after a hangup, as some
	// gateways do.
	KeepSidebandOnHangup bool
	// IgnorePings stops answering sideband pings, like a dead connection.
	IgnorePings bool
}

// NewFakeRealtime starts a FakeRealtime. Close it when done.
func NewFakeRealtime() *FakeRealtime {
	f := &FakeRealtime{conns: map[string]*websocket.Conn{}, pings: map[string]int{}, closed: map[string]bool{}, attached: map[string]chan struct{}{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/realtime/calls", f.createCall)
	mux.HandleFunc("POST /v1/realtime/calls/{id}/hangup", f.hangup)
	mux.HandleFunc("GET /v1/realtime", f.sideband)
	f.Server = httptest.NewServer(mux)
	return f
}

// URL is the base URL to configure as the service API URL.
func (f *FakeRealtime) URL() string { return f.Server.URL }

// Close closes the sideband connections and the server.
func (f *FakeRealtime) Close() {
	f.mu.Lock()
	for _, c := range f.conns {
		_ = c.Close()
	}
	f.mu.Unlock()
	f.Server.Close()
}

func (f *FakeRealtime) createCall(w http.ResponseWriter, r *http.Request) {
	fields, err := readMultipartFields(r)
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.sessions = append(f.sessions, json.RawMessage(fields["session"]))
	f.offers = append(f.offers, fields["sdp"])
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.nextID++
	id := fmt.Sprintf("rtc_test_%d", f.nextID)
	f.attached[id] = make(chan struct{})
	status, msg, omit, answer, delay := f.CallStatus, f.CallErrorMessage, f.OmitLocation, f.AnswerBody, f.CallDelay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}

	if status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg, "type": "server_error"}})
		return
	}
	if !omit {
		w.Header().Set("Location", "/v1/realtime/calls/"+id)
	}
	w.Header().Set("Content-Type", "application/sdp")
	w.WriteHeader(http.StatusCreated)
	if answer == "" {
		answer = FakeAnswerSDP
	}
	_, _ = w.Write([]byte(answer))
}

func readMultipartFields(r *http.Request) (map[string]string, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	fields := map[string]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return fields, nil
		}
		if err != nil {
			return nil, err
		}
		value, err := io.ReadAll(io.LimitReader(part, 1<<20))
		if err != nil {
			return nil, err
		}
		fields[part.FormName()] = string(value)
	}
}

func (f *FakeRealtime) hangup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f.mu.Lock()
	delay := f.HangupDelay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	f.mu.Lock()
	f.hangupAttempts = append(f.hangupAttempts, id)
	status := f.HangupStatus
	if status == 0 && f.HangupFailures > 0 {
		f.HangupFailures--
		status = http.StatusServiceUnavailable
	}
	if status != 0 && status != http.StatusOK {
		f.mu.Unlock()
		w.WriteHeader(status)
		return
	}
	f.hangups = append(f.hangups, id)
	conn := f.conns[id]
	keep := f.KeepSidebandOnHangup
	f.mu.Unlock()
	if conn != nil && !keep {
		_ = conn.Close()
	}
	w.WriteHeader(http.StatusOK)
}

func (f *FakeRealtime) sideband(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("call_id")
	f.mu.Lock()
	status := f.AttachStatus
	ready := f.attached[id]
	f.mu.Unlock()
	if status != 0 || ready == nil || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		if status == 0 {
			status = http.StatusNotFound
		}
		http.Error(w, "refused", status)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetPingHandler(func(data string) error {
		f.mu.Lock()
		f.pings[id]++
		ignore := f.IgnorePings
		f.mu.Unlock()
		if ignore {
			return nil
		}
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
	})
	f.mu.Lock()
	f.conns[id] = conn
	f.mu.Unlock()
	close(ready)
	// Drain so the server side sees a close.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			f.mu.Lock()
			f.closed[id] = true
			f.mu.Unlock()
			return
		}
	}
}

// WaitAttached waits for the server to attach the sideband of callID.
func (f *FakeRealtime) WaitAttached(callID string, timeout time.Duration) bool {
	f.mu.Lock()
	ready := f.attached[callID]
	f.mu.Unlock()
	if ready == nil {
		return false
	}
	select {
	case <-ready:
		return true
	case <-time.After(timeout):
		return false
	}
}

// SendEvent sends event on the sideband of callID.
func (f *FakeRealtime) SendEvent(callID string, event any) error {
	f.mu.Lock()
	conn := f.conns[callID]
	f.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("call %s has no sideband", callID)
	}
	return conn.WriteJSON(event)
}

// EndCall ends callID as the browser closing its connection does.
func (f *FakeRealtime) EndCall(callID string) {
	f.mu.Lock()
	conn := f.conns[callID]
	f.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// LastCallID returns the ID of the most recent call.
func (f *FakeRealtime) LastCallID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fmt.Sprintf("rtc_test_%d", f.nextID)
}

// Sessions returns the session configurations posted so far.
func (f *FakeRealtime) Sessions() []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]json.RawMessage(nil), f.sessions...)
}

// Offers returns the SDP offers posted so far.
func (f *FakeRealtime) Offers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.offers...)
}

// AuthHeaders returns the Authorization headers of call creations.
func (f *FakeRealtime) AuthHeaders() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auth...)
}

// Hangups returns the call IDs hung up so far.
func (f *FakeRealtime) Hangups() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hangups...)
}

// Pings returns how many WebSocket pings the sideband of callID received.
func (f *FakeRealtime) Pings(callID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pings[callID]
}

// SidebandClosed reports whether the sideband of callID has closed.
func (f *FakeRealtime) SidebandClosed(callID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed[callID]
}

// HangupAttempts returns every hangup request, failed ones included.
func (f *FakeRealtime) HangupAttempts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hangupAttempts...)
}

// CallCount returns how many calls were requested.
func (f *FakeRealtime) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nextID
}

// SessionUpdated builds a session.updated event as OpenAI sends it for a
// transcription session.
func SessionUpdated(model string, languages []string, prompt *string, turnDetection any) map[string]any {
	transcription := map[string]any{"model": model, "language": nil, "prompt": prompt}
	if languages != nil {
		transcription["languages"] = languages
	}
	return map[string]any{
		"type": "session.updated",
		"session": map[string]any{
			"type":   "transcription",
			"object": "realtime.transcription_session",
			"audio": map[string]any{"input": map[string]any{
				"transcription":   transcription,
				"noise_reduction": map[string]any{"type": "near_field"},
				"turn_detection":  turnDetection,
			}},
		},
	}
}

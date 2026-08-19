// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func testResults() []EvalLogLine {
	return []EvalLogLine{
		{Name: "TestA/[openai] first", Rubric: "rubric one", Output: "output one", Reasoning: "reasoning one", Score: 1, Pass: true},
		{Name: "TestB/[openai] second", Rubric: "rubric two", Output: "output two", Reasoning: "reasoning two", Score: 0, Pass: false},
		{Name: "TestC/[anthropic] third", Rubric: "rubric three", Output: "output three", Reasoning: "reasoning three", Score: 1, Pass: true},
	}
}

func sizedModel(t *testing.T, width, height int) model {
	t.Helper()

	next, _ := initialModel(testResults()).Update(tea.WindowSizeMsg{Width: width, Height: height})
	m, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return m
}

func send(t *testing.T, m model, msgs ...tea.Msg) model {
	t.Helper()

	for _, msg := range msgs {
		next, _ := m.Update(msg)
		typed, ok := next.(model)
		if !ok {
			t.Fatalf("Update returned %T, want model", next)
		}
		m = typed
	}
	return m
}

func keyPress(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Text: text}
}

func TestModelKeyNavigation(t *testing.T) {
	down := keyPress(tea.KeyDown, "")
	up := keyPress(tea.KeyUp, "")
	enter := keyPress(tea.KeyEnter, "")
	esc := keyPress(tea.KeyEscape, "")
	filter := keyPress('f', "f")

	tests := []struct {
		name           string
		keys           []tea.Msg
		expectCursor   int
		expectMode     viewMode
		expectFiltered int
	}{
		{
			name:           "starts at the first row in list view",
			expectCursor:   0,
			expectMode:     listView,
			expectFiltered: 3,
		},
		{
			name:           "moves down",
			keys:           []tea.Msg{down, down},
			expectCursor:   2,
			expectMode:     listView,
			expectFiltered: 3,
		},
		{
			name:           "stops at the last row",
			keys:           []tea.Msg{down, down, down, down},
			expectCursor:   2,
			expectMode:     listView,
			expectFiltered: 3,
		},
		{
			name:           "stops at the first row",
			keys:           []tea.Msg{down, up, up, up},
			expectCursor:   0,
			expectMode:     listView,
			expectFiltered: 3,
		},
		{
			name:           "enter opens the detail view",
			keys:           []tea.Msg{down, enter},
			expectCursor:   1,
			expectMode:     detailView,
			expectFiltered: 3,
		},
		{
			name:           "esc returns to the list view",
			keys:           []tea.Msg{enter, esc},
			expectCursor:   0,
			expectMode:     listView,
			expectFiltered: 3,
		},
		{
			name:           "filter keeps only failures and clamps the cursor",
			keys:           []tea.Msg{down, down, filter},
			expectCursor:   0,
			expectMode:     listView,
			expectFiltered: 1,
		},
		{
			name:           "filter toggles back to every result",
			keys:           []tea.Msg{filter, filter},
			expectCursor:   0,
			expectMode:     listView,
			expectFiltered: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := send(t, sizedModel(t, 100, 30), tt.keys...)

			if m.cursor != tt.expectCursor {
				t.Errorf("cursor = %d, want %d", m.cursor, tt.expectCursor)
			}
			if m.mode != tt.expectMode {
				t.Errorf("mode = %d, want %d", m.mode, tt.expectMode)
			}
			if len(m.filtered) != tt.expectFiltered {
				t.Errorf("filtered results = %d, want %d", len(m.filtered), tt.expectFiltered)
			}
		})
	}
}

func TestModelQuitOnKey(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{name: "q", key: keyPress('q', "q")},
		{name: "ctrl+c", key: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cmd := sizedModel(t, 100, 30).Update(tt.key)
			if cmd == nil {
				t.Fatal("expected a command, got nil")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Errorf("command produced %T, want tea.QuitMsg", cmd())
			}
		})
	}
}

func TestModelViewportTracksWindowSize(t *testing.T) {
	tests := []struct {
		name                      string
		sizes                     []tea.WindowSizeMsg
		expectWidth, expectHeight int
	}{
		{
			name:        "first size initializes the viewport",
			sizes:       []tea.WindowSizeMsg{{Width: 100, Height: 30}},
			expectWidth: 100, expectHeight: 27,
		},
		{
			name:        "resize updates the viewport",
			sizes:       []tea.WindowSizeMsg{{Width: 100, Height: 30}, {Width: 60, Height: 20}},
			expectWidth: 60, expectHeight: 17,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := initialModel(testResults())
			for _, size := range tt.sizes {
				m = send(t, m, size)
			}

			if !m.ready {
				t.Fatal("model is not ready after a window size message")
			}
			if got := m.viewport.Width(); got != tt.expectWidth {
				t.Errorf("viewport width = %d, want %d", got, tt.expectWidth)
			}
			if got := m.viewport.Height(); got != tt.expectHeight {
				t.Errorf("viewport height = %d, want %d", got, tt.expectHeight)
			}
		})
	}
}

func TestModelView(t *testing.T) {
	tests := []struct {
		name     string
		keys     []tea.Msg
		contains []string
	}{
		{
			name:     "list view shows the summary and every row",
			contains: []string{"3 tests", "2 passed", "1 failed", "TestA", "TestB", "TestC", "PASS", "FAIL"},
		},
		{
			name:     "detail view shows the selected result",
			keys:     []tea.Msg{keyPress(tea.KeyDown, ""), keyPress(tea.KeyEnter, "")},
			contains: []string{"Test Details", "TestB", "rubric two", "output two", "reasoning two"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := send(t, sizedModel(t, 120, 30), tt.keys...).View()

			if !v.AltScreen {
				t.Error("view does not request the alt screen")
			}
			if v.MouseMode != tea.MouseModeCellMotion {
				t.Errorf("view mouse mode = %d, want %d", v.MouseMode, tea.MouseModeCellMotion)
			}
			for _, want := range tt.contains {
				if !strings.Contains(v.Content, want) {
					t.Errorf("view content does not contain %q:\n%s", want, v.Content)
				}
			}
		})
	}
}

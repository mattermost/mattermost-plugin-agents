// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type viewMode int

const (
	listView viewMode = iota
	detailView
)

type model struct {
	results      []EvalLogLine
	filtered     []EvalLogLine
	cursor       int
	mode         viewMode
	showFailures bool
	width        int
	height       int
	viewport     viewport.Model
	ready        bool
}

func initialModel(results []EvalLogLine) model {
	m := model{
		results:      results,
		filtered:     results,
		cursor:       0,
		mode:         listView,
		showFailures: false,
	}
	return m
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		if !m.ready {
			// Initialize viewport once we have dimensions
			headerHeight := 2 // header + newline
			footerHeight := 1 // footer
			verticalMarginHeight := headerHeight + footerHeight

			m.viewport = viewport.New(
				viewport.WithWidth(msg.Width),
				viewport.WithHeight(msg.Height-verticalMarginHeight),
			)
			m.viewport.YPosition = headerHeight
			m.ready = true
		} else {
			m.viewport.SetWidth(msg.Width)
			m.viewport.SetHeight(msg.Height - 3) // header + footer + spacing
		}

		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "up", "k":
			if m.mode == listView {
				if m.cursor > 0 {
					m.cursor--
				}
			}

		case "down", "j":
			if m.mode == listView {
				if m.cursor < len(m.filtered)-1 {
					m.cursor++
				}
			}

		case "enter":
			if m.mode == listView && len(m.filtered) > 0 {
				m.mode = detailView
				// Set content for the viewport
				if m.ready {
					content := m.buildDetailContent()
					m.viewport.SetContent(content)
				}
			}

		case "esc":
			if m.mode == detailView {
				m.mode = listView
			}

		case "f":
			m.showFailures = !m.showFailures
			m.filtered = m.filterResults()
			if m.cursor >= len(m.filtered) {
				m.cursor = len(m.filtered) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
		}
	}

	// Handle viewport updates for detail view
	if m.mode == detailView && m.ready {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m model) filterResults() []EvalLogLine {
	if !m.showFailures {
		return m.results
	}

	var filtered []EvalLogLine
	for _, result := range m.results {
		if !result.Pass {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func (m model) View() tea.View {
	content := m.renderListView()
	if m.mode == detailView {
		content = m.renderDetailViewWithViewport()
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m model) buildDetailContent() string {
	if len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
		return "No results to display"
	}

	result := m.filtered[m.cursor]
	var b strings.Builder

	// Calculate available space
	width := m.width
	if width == 0 {
		width = 80 // fallback
	}

	// Content sections
	sectionStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("33"))

	contentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("255")).
		MarginLeft(2)

	// Rubric
	b.WriteString(sectionStyle.Render("Rubric:"))
	b.WriteString("\n")
	wrapped := wrapTextPreserveNewlines(result.Rubric, width-4)
	b.WriteString(contentStyle.Render(wrapped))
	b.WriteString("\n\n")

	// Output
	b.WriteString(sectionStyle.Render("Output:"))
	b.WriteString("\n")
	output := result.Output
	wrapped = wrapTextPreserveNewlines(output, width-4)
	b.WriteString(contentStyle.Render(wrapped))
	b.WriteString("\n\n")

	// Reasoning
	b.WriteString(sectionStyle.Render("Reasoning:"))
	b.WriteString("\n")
	wrapped = wrapTextPreserveNewlines(result.Reasoning, width-4)
	b.WriteString(contentStyle.Render(wrapped))
	b.WriteString("\n\n")

	// Result
	b.WriteString(sectionStyle.Render("Result:"))
	b.WriteString("\n")

	var resultStr string
	if result.Pass {
		resultStr = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")).
			Render(fmt.Sprintf("✓ PASS (Score: %.2f)", result.Score))
	} else {
		resultStr = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).
			Render(fmt.Sprintf("✗ FAIL (Score: %.2f)", result.Score))
	}

	b.WriteString(contentStyle.Render(resultStr))

	return b.String()
}

func (m model) renderDetailViewWithViewport() string {
	if len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
		return "No results to display"
	}

	result := m.filtered[m.cursor]
	var b strings.Builder

	// Calculate available space
	width := m.width
	if width == 0 {
		width = 80 // fallback
	}

	// Header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("62")).
		Padding(0, 1).
		Width(width)

	testName := result.Name
	maxNameLength := width - 20 // Account for "Test Details - " prefix
	if len(testName) > maxNameLength {
		testName = testName[:maxNameLength-3] + "..."
	}

	b.WriteString(headerStyle.Render(fmt.Sprintf("Test Details - %s", testName)))
	b.WriteString("\n")

	// Viewport content
	if m.ready {
		b.WriteString(m.viewport.View())
	}
	b.WriteString("\n")

	// Footer
	footerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Background(lipgloss.Color("236")).
		Padding(0, 1).
		Width(width)

	footer := "[Esc] Back [↑↓/j/k] Scroll [Page Up/Down] [Home/End] [f] Filter [q] Quit"
	b.WriteString(footerStyle.Render(footer))

	return b.String()
}

func (m model) renderListView() string {
	var b strings.Builder

	// Calculate available space
	width := m.width
	if width == 0 {
		width = 80 // fallback
	}

	// Header
	passed := 0
	failed := 0
	for _, result := range m.results {
		if result.Pass {
			passed++
		} else {
			failed++
		}
	}

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("62")).
		Padding(0, 1).
		Width(width)

	header := fmt.Sprintf("Evaluation Results - %d tests | %d passed | %d failed",
		len(m.results), passed, failed)

	if m.showFailures {
		header += " (showing failures only)"
	}

	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n\n")

	// Calculate column widths based on terminal width
	resultWidth := 8                            // "✗ FAIL" length
	testWidth := (width - resultWidth - 10) / 2 // Split remaining space
	rubricWidth := width - testWidth - resultWidth - 10

	// Table header
	tableHeaderStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("240"))

	headerLine := fmt.Sprintf("%-*s %-*s %s",
		testWidth, "TEST",
		rubricWidth, "RUBRIC",
		"RESULT")

	b.WriteString(tableHeaderStyle.Render(headerLine))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", width))
	b.WriteString("\n")

	// Results list
	for i, result := range m.filtered {
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}

		// Test name (truncated to fit column)
		testName := result.Name
		if len(testName) > testWidth-2 {
			testName = testName[:testWidth-5] + "..."
		}

		// Rubric (truncated to fit column)
		rubric := result.Rubric
		if len(rubric) > rubricWidth-2 {
			rubric = rubric[:rubricWidth-5] + "..."
		}

		// Result with color
		var resultStr string
		if result.Pass {
			resultStr = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2")).
				Render("✓ PASS")
		} else {
			resultStr = lipgloss.NewStyle().
				Foreground(lipgloss.Color("1")).
				Render("✗ FAIL")
		}

		// Format the line
		line := fmt.Sprintf("%s%-*s %-*s %s",
			cursor, testWidth-1, testName, rubricWidth, rubric, resultStr)

		// Highlight current row and fill to full width
		rowStyle := lipgloss.NewStyle().Width(width)
		if i == m.cursor {
			rowStyle = rowStyle.Background(lipgloss.Color("237"))
		}

		b.WriteString(rowStyle.Render(line))
		b.WriteString("\n")
	}

	// Add some space before footer
	b.WriteString("\n")

	// Footer
	footerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Background(lipgloss.Color("236")).
		Padding(0, 1).
		Width(width)

	footer := "[↑↓] Navigate  [Enter] View Details  [f] Filter Failures  [q] Quit"
	b.WriteString(footerStyle.Render(footer))

	return b.String()
}

func wrapTextPreserveNewlines(text string, width int) string {
	if width <= 0 {
		width = 80
	}

	// Split by existing newlines first
	paragraphs := strings.Split(text, "\n")
	var wrappedParagraphs []string

	for _, paragraph := range paragraphs {
		if strings.TrimSpace(paragraph) == "" {
			// Preserve empty lines
			wrappedParagraphs = append(wrappedParagraphs, "")
			continue
		}

		// Wrap each paragraph individually
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			wrappedParagraphs = append(wrappedParagraphs, "")
			continue
		}

		var lines []string
		currentLine := ""

		for _, word := range words {
			if len(currentLine)+len(word)+1 <= width {
				if currentLine == "" {
					currentLine = word
				} else {
					currentLine += " " + word
				}
			} else {
				if currentLine != "" {
					lines = append(lines, currentLine)
				}
				currentLine = word
			}
		}

		if currentLine != "" {
			lines = append(lines, currentLine)
		}

		wrappedParagraphs = append(wrappedParagraphs, strings.Join(lines, "\n"))
	}

	return strings.Join(wrappedParagraphs, "\n")
}

func runTUI(results []EvalLogLine) error {
	if len(results) == 0 {
		fmt.Println("No evaluation results found.")
		return nil
	}

	m := initialModel(results)
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}

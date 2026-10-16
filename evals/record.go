// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type EvalLogLine struct {
	Name      string  `json:"name"`
	Package   string  `json:"package,omitempty"`
	Timestamp string  `json:"timestamp"`
	RunNumber int     `json:"run_number"`
	Rubric    string  `json:"rubric"`
	Output    string  `json:"output"`
	Reasoning string  `json:"reasoning"`
	Score     float64 `json:"score"`
	Pass      bool    `json:"pass"`
	// Error is set when the run failed before any rubric could be graded,
	// for example because the provider rejected the request.
	Error string `json:"error,omitempty"`
	// Usage is set on the one line per run that carries its token usage,
	// cost, and latency; such lines have no rubric or error.
	Usage *UsageRecord `json:"usage,omitempty"`
}

type EvalResult struct {
	Rubric    string  `json:"rubric"`
	Output    string  `json:"output"`
	Reasoning string  `json:"reasoning"`
	Score     float64 `json:"score"`
	Pass      bool    `json:"pass"`
}

// evalFileLock is a mutex to prevent concurrent writes to the eval results file.
var evalFileLock sync.Mutex

// RecordScore records the score of an eval in a JSONL file.
func RecordScore(e *EvalT, result *EvalResult) {
	e.Helper()
	e.recorded++
	writeLogLine(e, EvalLogLine{
		Rubric:    result.Rubric,
		Output:    result.Output,
		Reasoning: result.Reasoning,
		Score:     result.Score,
		Pass:      result.Pass,
	})
}

// assertionsCheck is the check that every graded run records for the eval's
// assertions other than rubrics, such as required tool calls or valid
// citations, so a failing assertion is reported even when the rubrics pass.
const assertionsCheck = "Passes the eval's code assertions"

// failureLine returns the line that records a run's assertion failures: an
// error when the run failed before anything was graded, and otherwise the
// assertionsCheck result.
func failureLine(recorded int, failures []string) (EvalLogLine, bool) {
	summaries := make([]string, len(failures))
	for i, failure := range failures {
		summaries[i] = redactSecrets(summarizeFailure(failure))
	}
	switch {
	case recorded > 0:
		line := EvalLogLine{Rubric: assertionsCheck, Pass: len(failures) == 0, Reasoning: strings.Join(summaries, "\n")}
		if line.Pass {
			line.Score = 1
		}
		return line, true
	case len(failures) > 0:
		return EvalLogLine{Error: summaries[len(summaries)-1]}, true
	default:
		return EvalLogLine{}, false
	}
}

// recordUsage records the run's token usage, cost, and latency, if it made any
// LLM calls.
func recordUsage(e *EvalT) {
	e.Helper()
	usage := UsageRecord{Agent: e.agentMeter.snapshot(), Grader: e.graderMeter.snapshot()}
	if usage.Agent.Calls == 0 && usage.Grader.Calls == 0 {
		return
	}
	writeLogLine(e, EvalLogLine{Usage: &usage})
}

func writeLogLine(e *EvalT, log EvalLogLine) {
	e.Helper()
	log.Name = e.name
	log.Timestamp = time.Now().Format(time.RFC3339)
	log.RunNumber = e.runNumber

	dir, err := findCurrentModuleRoot()
	if err != nil {
		e.Fatalf("Failed to find module root: %v", err)
		return
	}
	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		if rel, relErr := filepath.Rel(dir, cwd); relErr == nil {
			log.Package = filepath.ToSlash(rel)
		}
	}
	file := filepath.Join(dir, "evals.jsonl")

	evalFileLock.Lock()
	defer evalFileLock.Unlock()

	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		e.Fatalf("Failed to open evals.jsonl: %v", err)
		return
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(log); err != nil {
		e.Fatalf("Failed to write to evals.jsonl: %v", err)
		return
	}
}

var testifyLabel = regexp.MustCompile(`^\s*([A-Za-z ]+):\s*\t(.*)$`)

// summarizeFailure reduces a testify failure message to its error and
// message sections, dropping the stack trace and test name.
func summarizeFailure(failure string) string {
	sections := map[string][]string{}
	label := ""
	for line := range strings.SplitSeq(failure, "\n") {
		if m := testifyLabel.FindStringSubmatch(line); m != nil {
			label = strings.TrimSpace(m[1])
			line = m[2]
		}
		if label == "" {
			continue
		}
		if text := strings.TrimSpace(line); text != "" {
			sections[label] = append(sections[label], text)
		}
	}

	if len(sections["Error"]) == 0 {
		return strings.TrimSpace(failure)
	}
	summary := strings.Join(sections["Error"], " ")
	if messages := sections["Messages"]; len(messages) > 0 {
		summary = strings.Join(messages, " ") + ": " + summary
	}
	return summary
}

// redactSecrets removes credential values that provider errors may echo back
// (keys, endpoints) so results can be published outside the job log.
func redactSecrets(s string) string {
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		if len(value) < 8 || !isSecretEnv(name) {
			continue
		}
		s = strings.ReplaceAll(s, value, "[REDACTED]")
	}
	return s
}

func isSecretEnv(name string) bool {
	name = strings.ToUpper(name)
	for _, suffix := range []string{"_KEY", "_KEY_ID", "_SECRET", "_TOKEN", "_ENDPOINT", "_API_URL"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func findCurrentModuleRoot() (string, error) {
	// Get the current working directory
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Find the module root
	moduleRoot := findModuleRoot(dir)
	if moduleRoot == "" {
		return "", errors.New("module root not found")
	}

	return moduleRoot, nil
}

// findProjectRoot finds the root of the project by looking for a go.mod file.
// Taken from go tool source cmd/go/internal/modload/init.go
func findModuleRoot(dir string) (roots string) {
	if dir == "" {
		panic("dir not set")
	}
	dir = filepath.Clean(dir)

	// Look for enclosing go.mod.
	for {
		if fi, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !fi.IsDir() {
			return dir
		}
		d := filepath.Dir(dir)
		if d == dir {
			break
		}
		dir = d
	}
	return ""
}

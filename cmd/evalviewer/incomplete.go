// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
)

var (
	// packageResult matches the line `go test` prints when a package finishes.
	packageResult = regexp.MustCompile(`^(ok|FAIL|\?)\s+(\S+)(\s+\[(build|setup) failed\])?`)
	// topLevelResult matches the result line of a top-level test; subtests are indented.
	topLevelResult = regexp.MustCompile(`^--- (PASS|FAIL|SKIP): (\S+)`)
	runLine        = regexp.MustCompile(`^=== RUN\s+(\S+)$`)
	goroutineLine  = regexp.MustCompile(`^goroutine \d+ \[`)
)

// incompletePackage is a package whose evals did not all run, so its results
// are partial: unlike failed evals, nothing in the results shows what is missing.
type incompletePackage struct {
	pkg    string
	reason string
}

func (p incompletePackage) String() string {
	return p.pkg + ": " + p.reason
}

// packageWatcher scans `go test` output for packages that did not finish
// their evals. Package output is not interleaved, so everything printed
// before a package's result line belongs to it.
//
// A failed package is complete only if tests failed, which is what failing
// evals do. Build and setup failures, a TestMain that exits, and a panic or
// timeout all end a package some other way. A failed test that never reached
// evals.Run (for example because suite setup failed) leaves no records, which
// unrecordedFailures checks against evals.jsonl.
type packageWatcher struct {
	mu      sync.Mutex
	partial []byte
	current packageOutput

	incomplete []incompletePackage
	// failed lists each package's failed top-level tests.
	failed map[string][]string
}

type packageOutput struct {
	started, finished map[string]bool
	failed            []string
	panicLine         bool
	panicked          bool
}

func (w *packageWatcher) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.line(string(w.partial[:i]))
		w.partial = w.partial[i+1:]
	}
}

func (w *packageWatcher) line(line string) {
	out := &w.current
	switch {
	case strings.HasPrefix(line, "panic: "):
		out.panicLine = true
	case out.panicLine && goroutineLine.MatchString(line):
		// A panic line alone can be program output; the stack trace of a real
		// one follows it.
		out.panicked = true
	}

	if m := runLine.FindStringSubmatch(line); m != nil && !strings.Contains(m[1], "/") {
		if out.started == nil {
			out.started = map[string]bool{}
		}
		out.started[m[1]] = true
		return
	}
	if m := topLevelResult.FindStringSubmatch(line); m != nil {
		if out.finished == nil {
			out.finished = map[string]bool{}
		}
		out.finished[m[2]] = true
		if m[1] == "FAIL" {
			out.failed = append(out.failed, m[2])
		}
		return
	}
	if m := packageResult.FindStringSubmatch(line); m != nil {
		w.packageDone(m[1], m[2], m[3] != "")
		w.current = packageOutput{}
	}
}

func (w *packageWatcher) packageDone(status, pkg string, buildOrSetupFailed bool) {
	if status != "FAIL" {
		return
	}
	out := w.current
	var reason string
	switch {
	case buildOrSetupFailed:
		reason = "failed to build or set up"
	case out.panicked:
		reason = "panicked or timed out"
	case len(out.unfinished()) > 0:
		reason = out.unfinished()[0] + " did not finish"
	case len(out.failed) == 0:
		reason = "failed without a failing test, for example because TestMain exited"
	default:
		if w.failed == nil {
			w.failed = map[string][]string{}
		}
		w.failed[pkg] = out.failed
		return
	}
	w.incomplete = append(w.incomplete, incompletePackage{pkg: pkg, reason: reason})
}

func (o packageOutput) unfinished() []string {
	var tests []string
	for test := range o.started {
		if !o.finished[test] {
			tests = append(tests, test)
		}
	}
	slices.Sort(tests)
	return tests
}

// unrecordedFailures returns the packages with a failed top-level test that
// recorded nothing in evals.jsonl. Failing evals always record a result, so
// such a test failed outside the evals, usually during setup.
func (w *packageWatcher) unrecordedFailures(records []EvalLogLine, modulePath string) []incompletePackage {
	w.mu.Lock()
	defer w.mu.Unlock()

	recorded := map[[2]string]bool{}
	for _, record := range records {
		test, _, _ := strings.Cut(record.Name, "/")
		recorded[[2]string{record.Package, test}] = true
	}

	var unrecorded []incompletePackage
	for pkg, tests := range w.failed {
		rel := strings.TrimPrefix(strings.TrimPrefix(pkg, modulePath), "/")
		if rel == "" {
			rel = "."
		}
		for _, test := range tests {
			if !recorded[[2]string{rel, test}] {
				unrecorded = append(unrecorded, incompletePackage{pkg: pkg, reason: test + " failed without recording a result"})
			}
		}
	}
	return unrecorded
}

func (w *packageWatcher) incompletePackages() []incompletePackage {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.incomplete
}

// reportIncomplete says which packages did not finish, as an error annotation
// when running in GitHub Actions.
func reportIncomplete(packages []incompletePackage) {
	if len(packages) == 0 {
		return
	}
	lines := make([]string, len(packages))
	for i, p := range packages {
		lines[i] = p.String()
	}
	fmt.Fprintf(os.Stderr, "\nEvals did not finish, so their results are partial:\n  %s\n", strings.Join(lines, "\n  "))
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		fmt.Printf("::error title=Evals did not finish::%s\n", escapeWorkflowCommand(strings.Join(lines, "; ")))
	}
}

func goModulePath() string {
	// A go.work makes `go list -m` print every workspace module.
	cmd := exec.Command("go", "list", "-m")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

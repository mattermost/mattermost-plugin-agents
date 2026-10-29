// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The outputs are what `go test -v` prints for each way a package can end.
const (
	passedPackage = `=== RUN   TestOKEval
=== RUN   TestOKEval/[openai]_a
--- PASS: TestOKEval (0.00s)
    --- PASS: TestOKEval/[openai]_a (0.00s)
PASS
ok  	example.com/m/okpkg	0.001s
`
	failedEval = `=== RUN   TestFailEval
=== RUN   TestFailEval/[openai]_a
    x_test.go:3: rubric failed
--- FAIL: TestFailEval (0.00s)
    --- FAIL: TestFailEval/[openai]_a (0.00s)
FAIL
FAIL	example.com/m/failpkg	0.002s
`
	testMainExit = `Failed to start postgres container
FAIL	example.com/m/mainexit	0.001s
`
	panicInTest = `=== RUN   TestFirstEval
--- FAIL: TestFirstEval (0.00s)
=== RUN   TestPanicEval
--- FAIL: TestPanicEval (0.00s)
panic: assignment to entry in nil map [recovered]
	panic: assignment to entry in nil map

goroutine 22 [running]:
testing.tRunner.func1.2({0x51b700, 0x56ea40})
FAIL	example.com/m/panicpkg	0.004s
`
	timeout = `=== RUN   TestSlowEval
panic: test timed out after 2s
running tests:
	TestSlowEval (2s)

goroutine 17 [running]:
testing.(*M).startAlarm.func1()
FAIL	example.com/m/timeoutpkg	2.006s
`
	processKilled = `=== RUN   TestFirstEval
--- FAIL: TestFirstEval (0.00s)
=== RUN   TestKilledEval
FAIL	example.com/m/killedpkg	0.100s
`
	printedPanicLine = `=== RUN   TestRecEval
panic: not a real panic
    x_test.go:3: x
--- FAIL: TestRecEval (0.00s)
FAIL
FAIL	example.com/m/recovered	0.002s
`
	buildFailure = "FAIL\texample.com/m/buildfail [build failed]\n"
	setupFailure = "FAIL\texample.com/m/setupfail [setup failed]\n"
)

func TestPackageWatcher(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []string
	}{
		{name: "passing package", output: passedPackage},
		{name: "failing evals are not incomplete", output: failedEval},
		{name: "a panic-looking line without a stack trace", output: printedPanicLine},
		{name: "build failure", output: buildFailure, want: []string{"example.com/m/buildfail"}},
		{name: "setup failure", output: setupFailure, want: []string{"example.com/m/setupfail"}},
		{name: "TestMain exits", output: testMainExit, want: []string{"example.com/m/mainexit"}},
		{name: "panic after a normal failure", output: panicInTest, want: []string{"example.com/m/panicpkg"}},
		{name: "timeout", output: timeout, want: []string{"example.com/m/timeoutpkg"}},
		{name: "process exits mid-test", output: processKilled, want: []string{"example.com/m/killedpkg"}},
		{
			name:   "only the package that ended abnormally is incomplete",
			output: passedPackage + timeout + failedEval,
			want:   []string{"example.com/m/timeoutpkg"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var w packageWatcher
			// Split writes mid-line, as a pipe may deliver them.
			for _, chunk := range []string{tc.output[:len(tc.output)/2], tc.output[len(tc.output)/2:]} {
				if _, err := w.Write([]byte(chunk)); err != nil {
					t.Fatal(err)
				}
			}
			var got []string
			for _, p := range w.incompletePackages() {
				got = append(got, p.pkg)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("incomplete packages = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUnrecordedFailures(t *testing.T) {
	const module = "example.com/m"
	output := failedEval + strings.ReplaceAll(failedEval, "failpkg", "other") +
		"=== RUN   TestSetupEval\n--- FAIL: TestSetupEval (0.00s)\nFAIL\nFAIL\texample.com/m/setuppkg\t0.1s\n"

	tests := []struct {
		name    string
		records []EvalLogLine
		want    []string
	}{
		{
			name: "every failed eval recorded a result",
			records: []EvalLogLine{
				{Name: "TestFailEval/[openai]_a/run_0", Package: "failpkg"},
				{Name: "TestFailEval/[openai]_a/run_0", Package: "other"},
				{Name: "TestSetupEval/[openai]_a/run_0", Package: "setuppkg"},
			},
		},
		{
			name: "a failure before evals.Run records nothing",
			records: []EvalLogLine{
				{Name: "TestFailEval/[openai]_a/run_0", Package: "failpkg"},
				{Name: "TestFailEval/[openai]_a/run_0", Package: "other"},
			},
			want: []string{"example.com/m/setuppkg"},
		},
		{
			name: "records of the same test name in another package do not count",
			records: []EvalLogLine{
				{Name: "TestFailEval/[openai]_a/run_0", Package: "failpkg"},
				{Name: "TestSetupEval/[openai]_a/run_0", Package: "setuppkg"},
			},
			want: []string{"example.com/m/other"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var w packageWatcher
			if _, err := w.Write([]byte(output)); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, p := range w.unrecordedFailures(tc.records, module) {
				got = append(got, p.pkg)
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("unrecorded failures = %v, want %v", got, tc.want)
			}
		})
	}
}

func exitError(t *testing.T, script string) error {
	t.Helper()
	err := exec.Command("sh", "-c", script).Run()
	if err == nil {
		t.Fatalf("%s succeeded", script)
	}
	return err
}

func TestEvalRunBroken(t *testing.T) {
	tests := []struct {
		name string
		run  evalRun
		want bool
	}{
		{name: "evals passed", run: evalRun{}},
		{name: "evals failed", run: evalRun{err: exitError(t, "exit 1")}},
		{name: "go test usage error", run: evalRun{err: exitError(t, "exit 2")}, want: true},
		{name: "go test killed", run: evalRun{err: exitError(t, "kill -9 $$")}, want: true},
		{name: "go test could not run", run: evalRun{err: errors.New("exec: go: executable file not found")}, want: true},
		{name: "a package did not finish", run: evalRun{err: exitError(t, "exit 1"), incomplete: []incompletePackage{{pkg: "p"}}}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.run.broken(); got != tc.want {
				t.Errorf("broken() = %v, want %v", got, tc.want)
			}
		})
	}
}

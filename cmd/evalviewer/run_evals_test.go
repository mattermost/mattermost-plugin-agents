// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fixtureEvals is a test file whose TestAEval records a failing result in the
// module's evals.jsonl, as a failing eval does, and then fails.
const fixtureEvals = `package p

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

var _ = syscall.Getpid

func TestAEval(t *testing.T) {
	wd, _ := os.Getwd()
	f, err := os.OpenFile(filepath.Join(wd, "..", "evals.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{\"name\":\"TestAEval/[openai]_a/run_0\",\"package\":\"p\",\"rubric\":\"r\",\"pass\":false}\n")
	f.Close()
	t.Error("eval failed")
}
`

func writeFixtureModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	goVersion := strings.TrimPrefix(runtime.Version(), "go")
	files["go.mod"] = "module example.com/fixture\n\ngo " + goVersion + "\n"
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRunEvals(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOFLAGS", "")

	tests := []struct {
		name         string
		extraTest    string
		unixOnly     bool
		wantBroken   bool
		wantComplete bool
	}{
		{
			name:         "failing evals that all ran are complete",
			wantComplete: true,
		},
		{
			// Without -v, go test prints no "=== RUN" line, so a test that
			// exits the process would look finished.
			name:       "a test that exits is caught without -v",
			extraTest:  "\nfunc TestBEval(t *testing.T) { os.Exit(3) }\n",
			wantBroken: true,
		},
		{
			// The killed go command never prints the package's result line.
			name:       "a go test killed by a signal is incomplete",
			extraTest:  "\nfunc TestBEval(t *testing.T) { _ = syscall.Kill(os.Getppid(), syscall.SIGKILL) }\n",
			unixOnly:   true,
			wantBroken: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unixOnly && runtime.GOOS == "windows" {
				t.Skip("needs Unix signals")
			}
			dir := writeFixtureModule(t, map[string]string{"p/p_test.go": fixtureEvals + tc.extraTest})
			t.Chdir(dir)

			run := runEvals([]string{"-run", "Eval$", "./p"})
			if got := run.broken(); got != tc.wantBroken {
				t.Errorf("broken() = %v, want %v (incomplete: %v, err: %v)", got, tc.wantBroken, run.incomplete, run.err)
			}

			lines, err := loadLines(filepath.Join(dir, "evals.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) != 1 {
				t.Fatalf("got %d result lines, want 1: %+v", len(lines), lines)
			}
			if lines[0].RunComplete != tc.wantComplete {
				t.Errorf("RunComplete = %v, want %v", lines[0].RunComplete, tc.wantComplete)
			}
		})
	}
}

func TestGoModulePathIgnoresWorkspace(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	t.Setenv("GOTOOLCHAIN", "local")
	goVersion := strings.TrimPrefix(runtime.Version(), "go")
	dir := writeFixtureModule(t, map[string]string{
		"other/go.mod": "module example.com/other\n\ngo " + goVersion + "\n",
		"go.work":      "go " + goVersion + "\n\nuse (\n\t.\n\t./other\n)\n",
	})
	t.Chdir(dir)

	if got := goModulePath(); got != "example.com/fixture" {
		t.Errorf("goModulePath() = %q, want %q", got, "example.com/fixture")
	}
}

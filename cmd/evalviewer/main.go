// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// EvalLogLine matches the structure from evals/record.go
type EvalLogLine struct {
	Name      string  `json:"name"`
	Timestamp string  `json:"timestamp"`
	RunNumber int     `json:"run_number"`
	Rubric    string  `json:"rubric"`
	Output    string  `json:"output"`
	Reasoning string  `json:"reasoning"`
	Score     float64 `json:"score"`
	Pass      bool    `json:"pass"`
}

var (
	// Flags for view command
	filename         string
	showOnlyFailures bool
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "evalviewer",
		Short: "Display evaluation results from evals.jsonl",
		Long: `evalviewer is a CLI tool to run evaluations and display results in a nice table format.

It can either run tests and display results, or view existing evaluation results.`,
	}

	var runCmd = &cobra.Command{
		Use:   "run [go test flags and args]",
		Short: "Run eval tests and display results",
		Long: `Run go test with GOEVALS=1 environment variable set, then automatically
find and display the evaluation results in a TUI.

All arguments after 'run' are passed directly to 'go test'.`,
		Example: `  evalviewer run -v ./conversations         # Run evals for conversations package
  evalviewer run -v ./...                   # Run all evals
  evalviewer run -v -cover ./conversations  # Run with test coverage`,
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			runCommand(args)
		},
	}

	var viewCmd = &cobra.Command{
		Use:   "view",
		Short: "Display existing evaluation results",
		Long:  `Display evaluation results from an existing evals.jsonl file in a TUI.`,
		Example: `  evalviewer view -file evals.jsonl         # View existing results
  evalviewer view -failures-only            # Show only failures`,
		Run: func(cmd *cobra.Command, args []string) {
			viewCommandWithFlags()
		},
	}

	var checkCmd = &cobra.Command{
		Use:   "check [go test flags and args]",
		Short: "Run eval tests and check results (CI-friendly, no TUI)",
		Long: `Run go test with GOEVALS=1 environment variable set, then check the results
and exit with status code 1 if any evaluations failed. This command is designed
for CI/CD pipelines and does not use the interactive TUI.

All arguments after 'check' are passed directly to 'go test'.`,
		Example: `  evalviewer check ./conversations           # Run and check evals for conversations
  evalviewer check ./...                     # Run and check all evals
  evalviewer check -v -cover ./...           # Run with verbose output and coverage`,
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			checkCommand(args)
		},
	}

	var commentCmd = &cobra.Command{
		Use:   "comment [go test flags and args]",
		Short: "Run eval tests and generate GitHub comment (CI-friendly)",
		Long: `Run go test with GOEVALS=1 environment variable set, then generate a GitHub-formatted
comment with the results. This command always exits with status code 0 (success) regardless
of test results, making it suitable for posting results as PR comments in CI.

The output is written to comment.md in the current directory.

All arguments after 'comment' are passed directly to 'go test'.`,
		Example: `  evalviewer comment ./conversations         # Run and generate comment for conversations
  evalviewer comment ./...                   # Run and generate comment for all evals
  evalviewer comment -v ./...                # Run with verbose output`,
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			commentCommand(args)
		},
	}

	// Add flags to view command
	viewCmd.Flags().StringVarP(&filename, "file", "f", "evals.jsonl", "Path to the evals.jsonl file")
	viewCmd.Flags().BoolVar(&showOnlyFailures, "failures-only", false, "Show only failed evaluations")

	// Add commands to root
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(viewCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(commentCmd)

	// Execute
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runCommand(args []string) {
	// Clean up old results
	if err := cleanupOldResults(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to clean old results: %v\n", err)
	}

	// Execute go test with GOEVALS=1
	fmt.Println("Running evaluations...")

	// Prepare go test command
	cmdArgs := []string{"test"}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command("go", cmdArgs...)
	cmd.Env = append(os.Environ(), "GOEVALS=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run command and show output
	if err := cmd.Run(); err != nil {
		fmt.Printf("\nTests completed with errors: %v\n", err)
	} else {
		fmt.Println("\nTests completed successfully.")
	}

	// Find and display results
	evalFile, err := findEvalsFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError finding evals.jsonl: %v\n", err)
		fmt.Println("You can view results manually with: evalviewer view -file /path/to/evals.jsonl")
		os.Exit(1)
	}

	// Display results with default settings
	results, err := loadResults(evalFile, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading results: %v\n", err)
		os.Exit(1)
	}

	// Launch TUI
	if err := runTUI(results); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

func viewCommandWithFlags() {
	results, err := loadResults(filename, false) // Don't pre-filter, let TUI handle it
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading results: %v\n", err)
		os.Exit(1)
	}

	// Launch TUI
	if err := runTUI(results); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

func checkCommand(args []string) {
	// Clean up old results
	if err := cleanupOldResults(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to clean old results: %v\n", err)
	}

	// Execute go test with GOEVALS=1
	fmt.Println("Running evaluations...")

	// Prepare go test command
	cmdArgs := []string{"test"}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command("go", cmdArgs...)
	cmd.Env = append(os.Environ(), "GOEVALS=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run command and show output
	testErr := cmd.Run()
	if testErr != nil {
		fmt.Printf("\nTests completed with errors: %v\n", testErr)
	} else {
		fmt.Println("\nTests completed successfully.")
	}

	// Find and check results
	evalFile, err := findEvalsFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError finding evals.jsonl: %v\n", err)
		fmt.Println("No evaluation results found to check.")
		// If tests failed but no evals file, exit with test error
		if testErr != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Load and check results
	results, err := loadResults(evalFile, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading results: %v\n", err)
		os.Exit(1)
	}

	// Print summary
	printSummary(results)

	// Exit with appropriate status code
	hasFailures := false
	for _, result := range results {
		if !result.Pass {
			hasFailures = true
			break
		}
	}

	if hasFailures || testErr != nil {
		os.Exit(1)
	}
}

func commentCommand(args []string) {
	// Clean up old results
	if err := cleanupOldResults(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to clean old results: %v\n", err)
	}

	// Execute go test with GOEVALS=1
	fmt.Println("Running evaluations...")

	// Prepare go test command
	cmdArgs := []string{"test"}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command("go", cmdArgs...)
	cmd.Env = append(os.Environ(), "GOEVALS=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run command and show output
	_ = cmd.Run() // Ignore test errors - we want to generate comment regardless

	// Find and check results
	evalFile, err := findEvalsFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError finding evals.jsonl: %v\n", err)
		fmt.Println("No evaluation results found to generate comment.")

		// Write empty comment to file
		if err := os.WriteFile("comment.md", []byte(""), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing comment.md: %v\n", err)
		}
		// Exit with success - we want the CI job to pass
		os.Exit(0)
	}

	// Load and check results
	results, err := loadResults(evalFile, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading results: %v\n", err)

		// Write empty comment to file
		if err := os.WriteFile("comment.md", []byte(""), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing comment.md: %v\n", err)
		}
		// Exit with success - we want the CI job to pass
		os.Exit(0)
	}

	// Generate GitHub comment
	comment := generateGitHubComment(results)

	// Write to file
	if err := os.WriteFile("comment.md", []byte(comment), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing comment.md: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✓ Generated comment (%d lines) → comment.md\n", len(strings.Split(comment, "\n")))

	// Always exit with success
	os.Exit(0)
}

func generateGitHubComment(results []EvalLogLine) string {
	if len(results) == 0 {
		return "⚠️ No evaluation results found."
	}

	var sb strings.Builder

	// Group results by provider
	providerStats := make(map[string]struct {
		passed   int
		failed   int
		failures []EvalLogLine
	})

	totalPassed := 0
	totalFailed := 0

	for _, result := range results {
		// Extract provider from test name (format: "ParentTest/[provider] test name")
		provider := "unknown"
		// Look for [provider] pattern anywhere in the name
		startIdx := strings.Index(result.Name, "[")
		if startIdx >= 0 {
			endIdx := strings.Index(result.Name[startIdx:], "]")
			if endIdx > 0 {
				provider = result.Name[startIdx+1 : startIdx+endIdx]
			}
		}

		stats := providerStats[provider]
		if result.Pass {
			stats.passed++
			totalPassed++
		} else {
			stats.failed++
			totalFailed++
			stats.failures = append(stats.failures, result)
		}
		providerStats[provider] = stats
	}

	// Overall summary
	total := totalPassed + totalFailed
	passRate := float64(totalPassed) / float64(total) * 100

	statusEmoji := "✅"
	if totalFailed > 0 {
		statusEmoji = "⚠️"
	}

	sb.WriteString(fmt.Sprintf("%s **Overall: %d/%d tests passed (%.1f%%)**\n\n", statusEmoji, totalPassed, total, passRate))

	// Per-provider summary table (no subheading - will be under provider section in aggregation)
	sb.WriteString("| Provider | Total | Passed | Failed | Pass Rate |\n")
	sb.WriteString("|----------|-------|--------|--------|----------|\n")

	for provider, stats := range providerStats {
		total := stats.passed + stats.failed
		passRate := float64(stats.passed) / float64(total) * 100
		emoji := "✅"
		if stats.failed > 0 {
			emoji = "⚠️"
		}
		sb.WriteString(fmt.Sprintf("| %s %s | %d | %d | %d | %.1f%% |\n",
			emoji, strings.ToUpper(provider), total, stats.passed, stats.failed, passRate))
	}

	// Failures section
	if totalFailed > 0 {
		sb.WriteString("\n### ❌ Failed Evaluations\n\n")
		sb.WriteString("<details>\n")
		sb.WriteString(fmt.Sprintf("<summary>Show %d failures</summary>\n\n", totalFailed))

		failureNum := 1
		for provider, stats := range providerStats {
			if len(stats.failures) > 0 {
				sb.WriteString(fmt.Sprintf("\n#### %s\n\n", strings.ToUpper(provider)))
				for _, failure := range stats.failures {
					sb.WriteString(fmt.Sprintf("**%d. %s**\n\n", failureNum, failure.Name))
					sb.WriteString(fmt.Sprintf("- **Score:** %.2f\n", failure.Score))
					if failure.Rubric != "" {
						sb.WriteString(fmt.Sprintf("- **Rubric:** %s\n", truncateString(failure.Rubric, 150)))
					}
					if failure.Reasoning != "" {
						sb.WriteString(fmt.Sprintf("- **Reason:** %s\n", truncateString(failure.Reasoning, 300)))
					}
					sb.WriteString("\n")
					failureNum++
				}
			}
		}

		sb.WriteString("</details>\n")
	}

	// No footer - aggregation script adds a single unified footer
	return sb.String()
}

func printSummary(results []EvalLogLine) {
	if len(results) == 0 {
		fmt.Println("\nNo evaluation results found.")
		return
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("EVALUATION RESULTS SUMMARY")
	fmt.Println(strings.Repeat("=", 80))

	// Group results by provider
	providerStats := make(map[string]struct {
		passed   int
		failed   int
		failures []EvalLogLine
	})

	totalPassed := 0
	totalFailed := 0

	for _, result := range results {
		// Extract provider from test name (format: "ParentTest/[provider] test name")
		provider := "unknown"
		// Look for [provider] pattern anywhere in the name
		startIdx := strings.Index(result.Name, "[")
		if startIdx >= 0 {
			endIdx := strings.Index(result.Name[startIdx:], "]")
			if endIdx > 0 {
				provider = result.Name[startIdx+1 : startIdx+endIdx]
			}
		}

		stats := providerStats[provider]
		if result.Pass {
			stats.passed++
			totalPassed++
		} else {
			stats.failed++
			totalFailed++
			stats.failures = append(stats.failures, result)
		}
		providerStats[provider] = stats
	}

	// Print per-provider statistics
	fmt.Println("\nResults by Provider:")
	fmt.Println(strings.Repeat("-", 80))
	for provider, stats := range providerStats {
		total := stats.passed + stats.failed
		fmt.Printf("  %s: %d tests (%d passed, %d failed)\n",
			strings.ToUpper(provider), total, stats.passed, stats.failed)
	}

	// Print overall statistics
	fmt.Println("\nOverall Statistics:")
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("Total Tests: %d\n", len(results))
	fmt.Printf("Passed:      %d\n", totalPassed)
	fmt.Printf("Failed:      %d\n", totalFailed)

	// Print failures grouped by provider
	if totalFailed > 0 {
		fmt.Println("\n" + strings.Repeat("-", 80))
		fmt.Println("FAILED EVALUATIONS:")
		fmt.Println(strings.Repeat("-", 80))

		failureNum := 1
		for provider, stats := range providerStats {
			if len(stats.failures) > 0 {
				fmt.Printf("\n%s Failures:\n", strings.ToUpper(provider))
				for _, failure := range stats.failures {
					fmt.Printf("\n%d. %s\n", failureNum, failure.Name)
					fmt.Printf("   Rubric: %s\n", truncateString(failure.Rubric, 70))
					fmt.Printf("   Score:  %.2f\n", failure.Score)
					if failure.Reasoning != "" {
						fmt.Printf("   Reason: %s\n", truncateString(failure.Reasoning, 250))
					}
					failureNum++
				}
			}
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func cleanupOldResults() error {
	// Look for evals.jsonl in current directory and parent directories
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	for {
		evalFile := filepath.Join(dir, "evals.jsonl")
		if _, err := os.Stat(evalFile); err == nil {
			// File exists, remove it
			fmt.Printf("Cleaning up old results: %s\n", evalFile)
			return os.Remove(evalFile)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root, no file found
			break
		}
		dir = parent
	}

	// No file found, nothing to clean up
	return nil
}

func findEvalsFile() (string, error) {
	// Look for evals.jsonl in current directory and parent directories
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		evalFile := filepath.Join(dir, "evals.jsonl")
		if _, err := os.Stat(evalFile); err == nil {
			return evalFile, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("evals.jsonl not found in current directory or parent directories")
}

func loadResults(filename string, showOnlyFailures bool) ([]EvalLogLine, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("error opening file %s: %v", filename, err)
	}
	defer file.Close()

	var results []EvalLogLine
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var result EvalLogLine
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing line: %v\n", err)
			continue
		}

		// Filter based on failures-only flag
		if showOnlyFailures && result.Pass {
			continue
		}

		results = append(results, result)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %v", err)
	}

	return results, nil
}

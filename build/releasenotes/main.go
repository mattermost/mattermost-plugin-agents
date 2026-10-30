// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// releasenotes writes the GitHub release notes for a Mattermost Agents release
// with an LLM. See README.md in this directory.
//
//	go run ./build/releasenotes [--base TAG] [--head REF] [--tag TAG] [--output FILE] [--print-prompt]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("releasenotes: ")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("releasenotes", flag.ContinueOnError)
	base := fs.String("base", "", "previous release tag; default: the previous release before --head")
	head := fs.String("head", "HEAD", "ref to release")
	tag := fs.String("tag", "", "tag being released; default: GITHUB_REF_NAME on tag builds, else a tag at --head")
	output := fs.String("output", "", "file to write; default: stdout")
	printPrompt := fs.Bool("print-prompt", false, "print the prompt and exit without calling the model")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*printPrompt && os.Getenv("ANTHROPIC_API_KEY") == "" {
		return errors.New("ANTHROPIC_API_KEY is not set")
	}
	repo := envOr("GITHUB_REPOSITORY", "mattermost/mattermost-plugin-agents")

	if *tag == "" {
		*tag = releaseTag(ctx, *head)
	}
	if *base == "" {
		var err error
		if *base, err = previousRelease(ctx, *head, *tag); err != nil {
			return err
		}
	}
	commits, err := commitsBetween(ctx, *base, *head)
	if err != nil {
		return err
	}
	log.Printf("range %s..%s (release %q): %d commits", *base, *head, *tag, len(commits))

	changes, err := loadChanges(ctx, repo, commits)
	if err != nil {
		return err
	}
	prompt := buildPrompt(repo, *tag, *base, changes)
	if *printPrompt {
		fmt.Printf("=== SYSTEM ===\n%s\n\n=== USER ===\n%s\n", systemPrompt, prompt)
		return nil
	}

	minServer, err := minServerVersion(ctx, *head)
	if err != nil {
		return err
	}
	model := envOr("RELEASE_NOTES_MODEL", defaultModel)
	log.Printf("generating notes with %s", model)
	notes, err := generateNotes(ctx, model, prompt)
	if err != nil {
		return err
	}
	content := composeNotes(minServer, notes)
	if *output == "" {
		_, err = os.Stdout.WriteString(content)
		return err
	}
	return os.WriteFile(*output, []byte(content), 0o644) //nolint:gosec // release notes are public
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

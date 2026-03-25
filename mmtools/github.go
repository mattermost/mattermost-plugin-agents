// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"github.com/google/go-github/v41/github"
	"github.com/mattermost/mattermost-plugin-agents/llm"
)

type GetGithubIssueArgs struct {
	RepoOwner string `jsonschema_description:"The owner of the repository to get issues from. Example: 'mattermost'"`
	RepoName  string `jsonschema_description:"The name of the repository to get issues from. Example: 'mattermost-plugin-agents'"`
	Number    int    `jsonschema_description:"The issue number to get. Example: '1'"`
}

var validGithubRepoName = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

func formatGithubIssue(issue *github.Issue) string {
	return fmt.Sprintf("Title: %s\nNumber: %d\nState: %s\nSubmitter: %s\nIs Pull Request: %v\nBody: %s",
		issue.GetTitle(),
		issue.GetNumber(),
		issue.GetState(),
		issue.User.GetLogin(),
		issue.IsPullRequest(),
		issue.GetBody())
}

func (p *MMToolProvider) toolGetGithubIssue(context *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args GetGithubIssueArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool GetGithubIssues: %w", err)
	}

	// Fail for over length repo owner or name.
	if len(args.RepoOwner) > 39 || len(args.RepoName) > 100 {
		return "invalid parameters to function", errors.New("invalid repo owner or repo name")
	}

	// Fail if repo owner or repo name contain invalid characters.
	if !validGithubRepoName.MatchString(args.RepoOwner) || !validGithubRepoName.MatchString(args.RepoName) {
		return "invalid parameters to function", errors.New("invalid repo owner or repo name")
	}

	// Fail for bad issue numbers.
	if args.Number < 1 {
		return "invalid parameters to function", errors.New("invalid issue number")
	}

	req, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("/github/api/v1/issue?owner=%s&repo=%s&number=%d",
			url.QueryEscape(args.RepoOwner),
			url.QueryEscape(args.RepoName),
			args.Number,
		),
		nil,
	)
	if err != nil {
		return "internal failure", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Mattermost-User-ID", context.RequestingUser.Id)

	resp := p.pluginAPI.PluginHTTP(req)
	if resp == nil {
		return "Error: unable to get issue, internal failure", errors.New("failed to get issue, response was nil")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		result, _ := io.ReadAll(resp.Body)
		return "Error: unable to get issue, internal failure", fmt.Errorf("failed to get issue, status code: %v\n body: %v", resp.Status, string(result))
	}

	var issue github.Issue
	err = json.NewDecoder(resp.Body).Decode(&issue)
	if err != nil {
		return "internal failure", fmt.Errorf("failed to decode response: %w", err)
	}

	return formatGithubIssue(&issue), nil
}

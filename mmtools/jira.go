// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/mattermost/mattermost-plugin-agents/llm"
)

type GetJiraIssueArgs struct {
	InstanceURL string   `jsonschema_description:"The URL of the Jira instance to get the issue from. Example: 'https://mattermost.atlassian.net'"`
	IssueKeys   []string `jsonschema_description:"The issue keys of the Jira issues to get. Example: 'MM-1234'"`
}

var validJiraIssueKey = regexp.MustCompile(`^([[:alnum:]]+)-([[:digit:]]+)$`)

var fetchedFields = []string{
	"summary",
	"description",
	"status",
	"assignee",
	"created",
	"updated",
	"issuetype",
	"labels",
	"reporter",
	"creator",
	"priority",
	"duedate",
	"timetracking",
	"comment",
}

func formatJiraIssue(issue *jira.Issue) string {
	result := strings.Builder{}
	result.WriteString("Issue Key: ")
	result.WriteString(issue.Key)
	result.WriteRune('\n')

	if issue.Fields != nil {
		result.WriteString("Summary: ")
		result.WriteString(issue.Fields.Summary)
		result.WriteRune('\n')

		result.WriteString("Description: ")
		result.WriteString(issue.Fields.Description)
		result.WriteRune('\n')

		result.WriteString("Status: ")
		if issue.Fields.Status != nil {
			result.WriteString(issue.Fields.Status.Name)
		} else {
			result.WriteString("Unknown")
		}
		result.WriteRune('\n')

		result.WriteString("Assignee: ")
		if issue.Fields.Assignee != nil {
			result.WriteString(issue.Fields.Assignee.DisplayName)
		} else {
			result.WriteString("Unassigned")
		}
		result.WriteRune('\n')

		result.WriteString("Created: ")
		result.WriteString(time.Time(issue.Fields.Created).Format(time.RFC1123))
		result.WriteRune('\n')

		result.WriteString("Updated: ")
		result.WriteString(time.Time(issue.Fields.Updated).Format(time.RFC1123))
		result.WriteRune('\n')

		if issue.Fields.Type.Name != "" {
			result.WriteString("Type: ")
			result.WriteString(issue.Fields.Type.Name)
			result.WriteRune('\n')
		}

		if issue.Fields.Labels != nil {
			result.WriteString("Labels: ")
			result.WriteString(strings.Join(issue.Fields.Labels, ", "))
			result.WriteRune('\n')
		}

		if issue.Fields.Reporter != nil {
			result.WriteString("Reporter: ")
			result.WriteString(issue.Fields.Reporter.DisplayName)
			result.WriteRune('\n')
		} else if issue.Fields.Creator != nil {
			result.WriteString("Creator: ")
			result.WriteString(issue.Fields.Creator.DisplayName)
			result.WriteRune('\n')
		}

		if issue.Fields.Priority != nil {
			result.WriteString("Priority: ")
			result.WriteString(issue.Fields.Priority.Name)
			result.WriteRune('\n')
		}

		if !time.Time(issue.Fields.Duedate).IsZero() {
			result.WriteString("Due Date: ")
			result.WriteString(time.Time(issue.Fields.Duedate).Format(time.RFC1123))
			result.WriteRune('\n')
		}

		if issue.Fields.TimeTracking != nil {
			if issue.Fields.TimeTracking.OriginalEstimate != "" {
				result.WriteString("Original Estimate: ")
				result.WriteString(issue.Fields.TimeTracking.OriginalEstimate)
				result.WriteRune('\n')
			}
			if issue.Fields.TimeTracking.TimeSpent != "" {
				result.WriteString("Time Spent: ")
				result.WriteString(issue.Fields.TimeTracking.TimeSpent)
				result.WriteRune('\n')
			}
			if issue.Fields.TimeTracking.RemainingEstimate != "" {
				result.WriteString("Remaining Estimate: ")
				result.WriteString(issue.Fields.TimeTracking.RemainingEstimate)
				result.WriteRune('\n')
			}
		}

		if issue.Fields.Comments != nil {
			for _, comment := range issue.Fields.Comments.Comments {
				result.WriteString(fmt.Sprintf("Comment from %s at %s: %s\n", comment.Author.DisplayName, comment.Created, comment.Body))
			}
		}
	}

	return result.String()
}

func (p *MMToolProvider) getPublicJiraIssues(instanceURL string, issueKeys []string) ([]jira.Issue, error) {
	client, err := jira.NewClient(p.httpClient, instanceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jira client: %w", err)
	}
	jql := fmt.Sprintf("key in (%s)", strings.Join(issueKeys, ","))
	issues, _, err := client.Issue.Search(jql, &jira.SearchOptions{Fields: fetchedFields})
	if err != nil {
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}
	if issues == nil {
		return nil, fmt.Errorf("failed to get issue: issue not found")
	}

	return issues, nil
}

func (p *MMToolProvider) toolGetJiraIssue(context *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args GetJiraIssueArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool GetJiraIssue: %w", err)
	}

	// Fail for over-length issue key. or doesn't look like an issue key
	for _, issueKey := range args.IssueKeys {
		if len(issueKey) > 50 || !validJiraIssueKey.MatchString(issueKey) {
			return "invalid parameters to function", errors.New("invalid issue key")
		}
	}

	issues, err := p.getPublicJiraIssues(args.InstanceURL, args.IssueKeys)
	if err != nil {
		return "internal failure", err
	}

	result := strings.Builder{}
	for i := range issues {
		result.WriteString(formatJiraIssue(&issues[i]))
		result.WriteString("------\n")
	}

	return result.String(), nil
}

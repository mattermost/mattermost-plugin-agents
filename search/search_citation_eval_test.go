// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"context"
	"regexp"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	embeddingmocks "github.com/mattermost/mattermost-plugin-ai/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-ai/evals"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	mmapimocks "github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestSearchCitationFormat verifies that the full search pipeline produces
// properly formatted permalink citations. It uses real code for enrichment,
// prompt building, and LLM completion — only the embedding search and
// mmapi.Client are mocked.
func TestSearchCitationFormat(t *testing.T) {
	const (
		siteURL   = "https://mattermost.example.com"
		teamID    = "team1"
		teamName  = "engineering"
		channelID = "ch1"
		userID    = "user1"
	)

	searchResults := []embeddings.SearchResult{
		{
			Document: embeddings.PostDocument{
				PostID:    "abc123def456ghij789klmno01",
				ChannelID: channelID,
				UserID:    "u1",
				Content:   "We decided to migrate the database to PostgreSQL 16 by end of Q2. The migration plan includes a phased rollout starting with staging.",
			},
			Score: 0.95,
		},
		{
			Document: embeddings.PostDocument{
				PostID:    "zzz999yyy888xxx777www666vv",
				ChannelID: channelID,
				UserID:    "u2",
				Content:   "The PostgreSQL migration test suite passed on staging. We are ready to proceed with production cutover next week.",
			},
			Score: 0.88,
		},
	}

	evalConfigs := []struct {
		name    string
		query   string
		rubrics []string
	}{
		{
			name:  "citations use permalink text and team URL format",
			query: "What is the database migration plan?",
			rubrics: []string{
				"all citation links use the exact markdown link text 'permalink' and not descriptive text",
				"citation URLs contain the team name 'engineering' in the path (e.g. /engineering/pl/) rather than using /_redirect/",
				"citation URLs include the query parameter view=citation",
				"the response references information from the provided search results about database migration",
			},
		},
	}

	for _, config := range evalConfigs {
		evals.Run(t, "search citation format: "+config.name, func(t *evals.EvalT) {
			// Mock embedding search to return canned results
			mockEmbedding := embeddingmocks.NewMockEmbeddingSearch(t.T)
			mockEmbedding.On("Search", mock.Anything, config.query, mock.Anything).
				Return(searchResults, nil)

			// Mock mmapi.Client for enrichment and prompt context
			mockClient := mmapimocks.NewMockClient(t.T)
			mockClient.On("GetChannel", channelID).Return(&model.Channel{
				Id:          channelID,
				DisplayName: "General",
				Type:        model.ChannelTypeOpen,
				TeamId:      teamID,
			}, nil)
			mockClient.On("GetUser", "u1").Return(&model.User{Id: "u1", Username: "alice"}, nil)
			mockClient.On("GetUser", "u2").Return(&model.User{Id: "u2", Username: "bob"}, nil)
			mockClient.On("GetTeam", teamID).Return(&model.Team{Id: teamID, Name: teamName}, nil)
			mockClient.On("GetConfig").Return(&model.Config{
				ServiceSettings: model.ServiceSettings{SiteURL: model.NewPointer(siteURL)},
			})

			// Create real Search service with eval LLM
			s := New(
				func() embeddings.EmbeddingSearch { return mockEmbedding },
				mockClient,
				t.Prompts,
				nil,
				nil,
			)

			// Create a bot wrapping the eval's real LLM
			bot := bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: "bot1"}, t.LLM)

			// Run the full SearchQuery code path
			resp, err := s.SearchQuery(context.Background(), userID, bot, config.query, "", channelID, 5)
			require.NoError(t.T, err)
			require.NotEmpty(t.T, resp.Answer)

			t.Logf("LLM answer:\n%s", resp.Answer)

			// Deterministic checks: verify citation URL format
			citationRegex := regexp.MustCompile(`\[permalink\]\([^)]*\?view=citation\)`)
			citations := citationRegex.FindAllString(resp.Answer, -1)
			assert.GreaterOrEqual(t.T, len(citations), 1, "Expected at least one properly formatted citation")

			for _, cite := range citations {
				assert.Contains(t.T, cite, "/"+teamName+"/pl/", "Citation should use team name, not _redirect: %s", cite)
				assert.Contains(t.T, cite, siteURL, "Citation should include siteURL: %s", cite)
			}

			// LLM rubric checks
			for _, rubric := range config.rubrics {
				evals.LLMRubricT(t, rubric, resp.Answer)
			}
		})
	}
}

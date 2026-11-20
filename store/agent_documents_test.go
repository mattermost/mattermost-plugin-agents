// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func saveTestDocument(t *testing.T, s *Store, createdBy, name, content string) *AgentDocument {
	t.Helper()
	doc := &AgentDocument{
		Name:          name,
		MimeType:      "text/plain",
		Content:       []byte(content),
		ExtractedText: content,
		CreatedBy:     createdBy,
	}
	require.NoError(t, s.SaveAgentDocument(doc))
	return doc
}

func documentRef(doc *AgentDocument, name string) llm.AgentDocument {
	return llm.AgentDocument{ID: doc.ID, Name: name, MimeType: doc.MimeType, Size: doc.Size, SHA256: doc.SHA256, TextRunes: doc.TextRunes}
}

func TestAgentDocumentBlobs(t *testing.T) {
	s := setupVersionTestStore(t)

	content := "Refunds within 30 days. Ünïcode."
	doc := saveTestDocument(t, s, "user-1", "policy.txt", content)
	sum := sha256.Sum256([]byte(content))
	const unknownID = "missingmissingmissingmiss1"

	assert.Len(t, doc.ID, 26)
	assert.Equal(t, hex.EncodeToString(sum[:]), doc.SHA256, "save computes the checksum")
	assert.Equal(t, int64(len(content)), doc.Size)
	assert.Equal(t, 32, doc.TextRunes)
	assert.NotZero(t, doc.CreateAt)

	getTests := []struct {
		name string
		id   string
		want *AgentDocument
	}{
		{name: "get returns content and text", id: doc.ID, want: doc},
		{name: "get of an unknown document returns nil", id: unknownID},
	}
	for _, tt := range getTests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.GetAgentDocument(tt.id)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	lookupTests := []struct {
		name      string
		ids       []string
		canceled  bool
		wantTexts map[string]string
		wantInfos []string
	}{
		{
			name:      "known ids are returned and unknown ones skipped",
			ids:       []string{doc.ID, unknownID},
			wantTexts: map[string]string{doc.ID: content},
			wantInfos: []string{doc.ID},
		},
		{name: "texts are not read once the context is done", ids: []string{doc.ID}, canceled: true},
	}
	for _, tt := range lookupTests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.canceled {
				cancel()
			}

			texts, err := s.GetAgentDocumentTexts(ctx, tt.ids)
			if tt.canceled {
				require.ErrorIs(t, err, context.Canceled)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantTexts, texts)

			infos, err := s.GetAgentDocumentInfos(tt.ids)
			require.NoError(t, err)
			require.Len(t, infos, len(tt.wantInfos))
			for _, id := range tt.wantInfos {
				info := infos[id]
				assert.Nil(t, info.Content, "infos omit content")
				assert.Empty(t, info.ExtractedText, "infos omit text")
				assert.Equal(t, doc.SHA256, info.SHA256)
				assert.Equal(t, "user-1", info.CreatedBy)
				assert.Equal(t, doc.TextRunes, info.TextRunes)
			}
		})
	}

	saveTests := []struct {
		name      string
		createdBy string
		mimeType  string
		content   string
		sameID    bool
	}{
		{name: "same uploader, content and type reuses the document", createdBy: "user-1", mimeType: "text/plain", content: content, sameID: true},
		{name: "another uploader gets a new document", createdBy: "user-2", mimeType: "text/plain", content: content},
		{name: "another type gets a new document", createdBy: "user-1", mimeType: "text/markdown", content: content},
		{name: "other content gets a new document", createdBy: "user-1", mimeType: "text/plain", content: content + "!"},
	}
	for _, tt := range saveTests {
		t.Run(tt.name, func(t *testing.T) {
			again := &AgentDocument{Name: "renamed.txt", MimeType: tt.mimeType, Content: []byte(tt.content), ExtractedText: tt.content, CreatedBy: tt.createdBy}
			require.NoError(t, s.SaveAgentDocument(again))
			if tt.sameID {
				assert.Equal(t, doc.ID, again.ID)
				stored, err := s.GetAgentDocument(again.ID)
				require.NoError(t, err)
				assert.Equal(t, "policy.txt", stored.Name, "the stored document keeps its name")
			} else {
				assert.NotEqual(t, doc.ID, again.ID)
			}
		})
	}
}

func TestAgentDocumentReferences(t *testing.T) {
	s := setupVersionTestStore(t)
	handbook := saveTestDocument(t, s, "creator-1", "handbook.txt", "Handbook text")
	faq := saveTestDocument(t, s, "creator-1", "faq.txt", "FAQ text")

	agent := testAgent("creator-1", "doc-agent", "Doc Agent")
	agent.Documents = []llm.AgentDocument{documentRef(handbook, "Handbook")}
	require.NoError(t, s.CreateAgent(agent, AgentVersionMeta{ActorID: "creator-1", Source: AgentVersionSourceCreate}))

	v1Docs := []llm.AgentDocument{documentRef(handbook, "Handbook")}
	v2Docs := []llm.AgentDocument{documentRef(faq, "FAQ"), documentRef(handbook, "Employee handbook")}

	// Each step follows the previous one: version N is the Nth step.
	steps := []struct {
		name          string
		documents     func() []llm.AgentDocument // nil leaves the created agent as it is
		source        AgentVersionSource
		restoredFrom  int
		wantDocuments []llm.AgentDocument
		wantChanged   []string
	}{
		{name: "create stores the references", wantDocuments: v1Docs, wantChanged: []string{}},
		{name: "update adds, renames and reorders", documents: func() []llm.AgentDocument { return v2Docs }, source: AgentVersionSourceUpdate, wantDocuments: v2Docs, wantChanged: []string{"documents"}},
		{name: "update removes all", documents: func() []llm.AgentDocument { return nil }, source: AgentVersionSourceUpdate, wantDocuments: []llm.AgentDocument{}, wantChanged: []string{"documents"}},
		{name: "restoring version 2 brings its documents back", documents: func() []llm.AgentDocument { return v2Docs }, source: AgentVersionSourceRestore, restoredFrom: 2, wantDocuments: v2Docs, wantChanged: []string{"documents"}},
	}
	for i, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			if step.documents != nil {
				current, err := s.GetAgent(agent.ID)
				require.NoError(t, err)
				current.Documents = step.documents()
				require.NoError(t, s.UpdateAgent(current, AgentVersionMeta{ActorID: "creator-1", Source: step.source, RestoredFromVersion: step.restoredFrom}))
			}

			got, err := s.GetAgent(agent.ID)
			require.NoError(t, err)
			assert.NotNil(t, got.Documents, "an agent without documents lists an empty array")
			assert.Equal(t, step.wantDocuments, got.Documents)

			detail, err := s.GetAgentVersion(agent.ID, i+1)
			require.NoError(t, err)
			require.NotNil(t, detail)
			assert.Equal(t, step.wantDocuments, detail.Config.Documents)
			assert.Equal(t, step.wantChanged, detail.ChangedFields)
		})
	}

	referenceTests := []struct {
		name    string
		agentID string
		want    []llm.AgentDocument
	}{
		{
			name:    "references list the current row, then versions newest first",
			agentID: agent.ID,
			want: []llm.AgentDocument{
				documentRef(faq, "FAQ"), documentRef(handbook, "Employee handbook"), // current row, restored from version 2
				documentRef(faq, "FAQ"), documentRef(handbook, "Employee handbook"), // version 4
				documentRef(faq, "FAQ"), documentRef(handbook, "Employee handbook"), // version 2
				documentRef(handbook, "Handbook"), // version 1
			},
		},
		{name: "an unknown agent has no references", agentID: "missingmissingmissingmiss1"},
	}
	for _, tt := range referenceTests {
		t.Run(tt.name, func(t *testing.T) {
			refs, err := s.ListAgentDocumentReferences(tt.agentID)
			require.NoError(t, err)
			assert.Equal(t, tt.want, refs)
		})
	}
}

// Version snapshots are immutable, so one holding a value Postgres jsonb
// cannot represent (\u0000) must not break document references forever.
func TestAgentDocumentReferencesWithNULInSnapshot(t *testing.T) {
	s := setupVersionTestStore(t)
	handbook := saveTestDocument(t, s, "creator-1", "handbook.txt", "Handbook text")

	agent := testAgent("creator-1", "nul-agent", "NUL Agent")
	agent.Documents = []llm.AgentDocument{documentRef(handbook, "Handbook")}
	agent.EnabledMCPTools = []llm.EnabledMCPTool{{ServerOrigin: "https://mcp.example.com", ToolName: "bad\x00tool"}}
	require.NoError(t, s.CreateAgent(agent, AgentVersionMeta{ActorID: "creator-1", Source: AgentVersionSourceCreate}))

	var raw string
	require.NoError(t, s.db.Get(&raw, `SELECT Config FROM Agents_AgentVersions WHERE AgentID = $1`, agent.ID))
	require.Contains(t, raw, `\u0000`, "the snapshot holds an escaped NUL")

	refs, err := s.ListAgentDocumentReferences(agent.ID)
	require.NoError(t, err)
	assert.Equal(t, []llm.AgentDocument{
		documentRef(handbook, "Handbook"), // current row
		documentRef(handbook, "Handbook"), // version 1
	}, refs)
}

// Snapshots recorded before agents had documents lack the key; that must not
// read as a change, or every agent would get a spurious version on upgrade.
func TestAgentVersionSnapshotsWithoutDocuments(t *testing.T) {
	s := setupVersionTestStore(t)
	agent := testAgent("creator-1", "legacy-docs", "Legacy Docs")
	require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))

	var raw string
	require.NoError(t, s.db.Get(&raw, `SELECT Config FROM Agents_AgentVersions WHERE AgentID = $1`, agent.ID))
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &fields))
	delete(fields, "documents")
	legacy, err := json.Marshal(fields)
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE Agents_AgentVersions SET Config = $1 WHERE AgentID = $2`, string(legacy), agent.ID)
	require.NoError(t, err)

	backfilled, err := s.BackfillAgentVersions()
	require.NoError(t, err)
	assert.Zero(t, backfilled, "a snapshot without documents matches an agent without documents")

	v1, err := s.GetAgentVersion(agent.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, []llm.AgentDocument{}, v1.Config.Documents)

	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	fetched.DisplayName = "Renamed"
	require.NoError(t, s.UpdateAgent(fetched, SystemAgentVersionMeta()))
	v2, err := s.GetAgentVersion(agent.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"displayName"}, v2.ChangedFields)

	refs, err := s.ListAgentDocumentReferences(agent.ID)
	require.NoError(t, err)
	assert.Empty(t, refs)
}

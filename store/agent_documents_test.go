// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
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

	t.Run("save computes metadata", func(t *testing.T) {
		assert.Len(t, doc.ID, 26)
		assert.Equal(t, hex.EncodeToString(sum[:]), doc.SHA256)
		assert.Equal(t, int64(len(content)), doc.Size)
		assert.Equal(t, 32, doc.TextRunes)
		assert.NotZero(t, doc.CreateAt)
	})

	t.Run("get returns content and text", func(t *testing.T) {
		got, err := s.GetAgentDocument(doc.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, doc, got)
	})

	t.Run("get of an unknown document returns nil", func(t *testing.T) {
		got, err := s.GetAgentDocument("missingmissingmissingmiss1")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("infos omit content and skip unknown ids", func(t *testing.T) {
		infos, err := s.GetAgentDocumentInfos([]string{doc.ID, "missingmissingmissingmiss1"})
		require.NoError(t, err)
		require.Len(t, infos, 1)
		info := infos[doc.ID]
		assert.Nil(t, info.Content)
		assert.Empty(t, info.ExtractedText)
		assert.Equal(t, doc.SHA256, info.SHA256)
		assert.Equal(t, "user-1", info.CreatedBy)
		assert.Equal(t, doc.TextRunes, info.TextRunes)
	})

	t.Run("texts skip unknown ids", func(t *testing.T) {
		texts, err := s.GetAgentDocumentTexts([]string{doc.ID, "missingmissingmissingmiss1"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{doc.ID: content}, texts)
	})

	tests := []struct {
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
	for _, tt := range tests {
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

	t.Run("documents round trip through the agent row", func(t *testing.T) {
		fetched, err := s.GetAgent(agent.ID)
		require.NoError(t, err)
		assert.Equal(t, agent.Documents, fetched.Documents)
	})

	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	fetched.Documents = []llm.AgentDocument{documentRef(faq, "FAQ"), documentRef(handbook, "Employee handbook")}
	require.NoError(t, s.UpdateAgent(fetched, AgentVersionMeta{ActorID: "creator-1", Source: AgentVersionSourceUpdate}))

	fetched, err = s.GetAgent(agent.ID)
	require.NoError(t, err)
	fetched.Documents = nil
	require.NoError(t, s.UpdateAgent(fetched, AgentVersionMeta{ActorID: "creator-1", Source: AgentVersionSourceUpdate}))

	t.Run("an agent without documents lists an empty array", func(t *testing.T) {
		got, getErr := s.GetAgent(agent.ID)
		require.NoError(t, getErr)
		assert.NotNil(t, got.Documents)
		assert.Empty(t, got.Documents)
	})

	versionTests := []struct {
		name          string
		version       int
		documents     []llm.AgentDocument
		changedFields []string
	}{
		{name: "create snapshot", version: 1, documents: []llm.AgentDocument{documentRef(handbook, "Handbook")}, changedFields: []string{}},
		{name: "update adds, renames and reorders", version: 2, documents: []llm.AgentDocument{documentRef(faq, "FAQ"), documentRef(handbook, "Employee handbook")}, changedFields: []string{"documents"}},
		{name: "update removes all", version: 3, documents: []llm.AgentDocument{}, changedFields: []string{"documents"}},
	}
	for _, tt := range versionTests {
		t.Run(tt.name, func(t *testing.T) {
			detail, getErr := s.GetAgentVersion(agent.ID, tt.version)
			require.NoError(t, getErr)
			require.NotNil(t, detail)
			assert.Equal(t, tt.documents, detail.Config.Documents)
			assert.Equal(t, tt.changedFields, detail.ChangedFields)
		})
	}

	t.Run("references list the current row, then versions newest first", func(t *testing.T) {
		refs, listErr := s.ListAgentDocumentReferences(agent.ID)
		require.NoError(t, listErr)
		assert.Equal(t, []llm.AgentDocument{
			documentRef(faq, "FAQ"), documentRef(handbook, "Employee handbook"), // version 2
			documentRef(handbook, "Handbook"), // version 1
		}, refs)
	})

	t.Run("restoring a version brings its documents back", func(t *testing.T) {
		v2, getErr := s.GetAgentVersion(agent.ID, 2)
		require.NoError(t, getErr)
		current, getErr := s.GetAgent(agent.ID)
		require.NoError(t, getErr)
		current.Documents = v2.Config.Documents
		require.NoError(t, s.UpdateAgent(current, AgentVersionMeta{ActorID: "creator-1", Source: AgentVersionSourceRestore, RestoredFromVersion: 2}))

		restored, getErr := s.GetAgent(agent.ID)
		require.NoError(t, getErr)
		assert.Equal(t, v2.Config.Documents, restored.Documents)
		v4, getErr := s.GetAgentVersion(agent.ID, 4)
		require.NoError(t, getErr)
		assert.Equal(t, []string{"documents"}, v4.ChangedFields)
	})

	t.Run("an unknown agent has no references", func(t *testing.T) {
		refs, listErr := s.ListAgentDocumentReferences("missingmissingmissingmiss1")
		require.NoError(t, listErr)
		assert.Empty(t, refs)
	})
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

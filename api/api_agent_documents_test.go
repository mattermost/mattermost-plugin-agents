// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentdocs"
	"github.com/mattermost/mattermost-plugin-agents/v2/agentexport"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const otherUserID = "othr12345678901234567890ab"

func multipartDocumentBody(t *testing.T, filename string, content []byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = fw.Write(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return &buf, w.FormDataContentType()
}

func doUploadDocument(e *TestEnvironment, body io.Reader, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/agents/documents", body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Mattermost-User-Id", testUserID)
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, req)
	return recorder
}

// seedAgentDocument stores a text document whose content is its extracted
// text and returns it as an agent references it.
func seedAgentDocument(t *testing.T, e *TestEnvironment, name, text, createdBy string) llm.AgentDocument {
	t.Helper()
	mimeType, err := agentdocs.MimeTypeForName(name)
	require.NoError(t, err)
	doc := &store.AgentDocument{Name: name, MimeType: mimeType, Content: []byte(text), ExtractedText: text, CreatedBy: createdBy}
	require.NoError(t, e.agentStore.SaveAgentDocument(doc))
	return agentDocumentFromStored(doc, name)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestUploadAgentDocument(t *testing.T) {
	tests := []struct {
		name           string
		canCreate      bool
		managesAgent   bool
		filename       string
		content        []byte
		noFile         bool
		expectedStatus int
		errorContains  string
		expected       *llm.AgentDocument
		expectedText   string
	}{
		{
			name:           "markdown is stored for the uploader with its normalized text",
			canCreate:      true,
			filename:       "notes.md",
			content:        []byte("# Title\r\nbody\r\n"),
			expectedStatus: http.StatusCreated,
			expected: &llm.AgentDocument{
				Name: "notes.md", MimeType: agentdocs.MimeTypeMarkdown, Size: 15,
				SHA256: sha256Hex([]byte("# Title\r\nbody\r\n")), TextRunes: 12,
			},
			expectedText: "# Title\nbody",
		},
		{
			name:           "agent managers without create permission may upload",
			managesAgent:   true,
			filename:       "data.csv",
			content:        []byte("a,b\n1,2\n"),
			expectedStatus: http.StatusCreated,
			expected: &llm.AgentDocument{
				Name: "data.csv", MimeType: agentdocs.MimeTypeCSV, Size: 8,
				SHA256: sha256Hex([]byte("a,b\n1,2\n")), TextRunes: 7,
			},
			expectedText: "a,b\n1,2",
		},
		{
			name:           "users who can neither create nor manage agents are rejected",
			filename:       "notes.md",
			content:        []byte("text"),
			expectedStatus: http.StatusForbidden,
			errorContains:  "permission to upload agent documents",
		},
		{
			name:           "unsupported type",
			canCreate:      true,
			filename:       "tool.exe",
			content:        []byte("MZ"),
			expectedStatus: http.StatusBadRequest,
			errorContains:  `"tool.exe" is not a supported document type`,
		},
		{
			name:           "file just over the size limit",
			canCreate:      true,
			filename:       "big.txt",
			content:        bytes.Repeat([]byte("a"), agentdocs.MaxDocumentBytes+1),
			expectedStatus: http.StatusRequestEntityTooLarge,
			errorContains:  "document is too large (max 10 MiB)",
		},
		{
			name:           "body over the upload limit",
			canCreate:      true,
			filename:       "huge.txt",
			content:        bytes.Repeat([]byte("a"), maxAgentDocumentUploadBytes+1),
			expectedStatus: http.StatusRequestEntityTooLarge,
			errorContains:  "document is too large (max 10 MiB)",
		},
		{
			name:           "document without text",
			canCreate:      true,
			filename:       "blank.txt",
			content:        []byte(" \r\n\t \n"),
			expectedStatus: http.StatusBadRequest,
			errorContains:  `no extractable text found in "blank.txt"`,
		},
		{
			name:           "text file that is not UTF-8",
			canCreate:      true,
			filename:       "latin1.txt",
			content:        []byte{'c', 'a', 'f', 0xe9},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "not valid UTF-8",
		},
		{
			name:           "missing file field",
			canCreate:      true,
			noFile:         true,
			expectedStatus: http.StatusBadRequest,
			errorContains:  "missing or invalid document file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(tt.canCreate)
			e.mockAPI.On("HasPermissionTo", testUserID, mock.Anything).Return(false).Maybe()
			if tt.managesAgent {
				e.agentStore.agents["agent-1"] = &llm.BotConfig{ID: "agent-1", CreatorID: testUserID, Name: "my-agent"}
			}

			var body io.Reader
			var contentType string
			if tt.noFile {
				var buf bytes.Buffer
				w := multipart.NewWriter(&buf)
				require.NoError(t, w.WriteField("other", "value"))
				require.NoError(t, w.Close())
				body, contentType = &buf, w.FormDataContentType()
			} else {
				body, contentType = multipartDocumentBody(t, tt.filename, tt.content)
			}
			recorder := doUploadDocument(e, body, contentType)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())

			if tt.expected == nil {
				assert.Contains(t, decodeAgentError(t, recorder), tt.errorContains)
				assert.Empty(t, e.agentStore.documents, "a rejected upload stores nothing")
				return
			}
			var resp llm.AgentDocument
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
			require.NotEmpty(t, resp.ID)
			want := *tt.expected
			want.ID = resp.ID
			assert.Equal(t, want, resp)

			stored := e.agentStore.documents[resp.ID]
			require.NotNil(t, stored)
			assert.Equal(t, testUserID, stored.CreatedBy)
			assert.Equal(t, tt.content, stored.Content)
			assert.Equal(t, tt.expectedText, stored.ExtractedText)
		})
	}
}

func TestAuditUploadAgentDocument(t *testing.T) {
	const (
		plantedName    = "planted-document-name-sentinel.txt"
		plantedContent = "planted-document-content-sentinel"
	)

	tests := []struct {
		name           string
		canCreate      bool
		content        string
		expectedStatus int
		validateRecord func(t *testing.T, e *TestEnvironment, rec *model.AuditRecord)
	}{
		{
			name:           "success records the document id, type and size only",
			canCreate:      true,
			content:        plantedContent,
			expectedStatus: http.StatusCreated,
			validateRecord: func(t *testing.T, e *TestEnvironment, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				require.Len(t, e.agentStore.documents, 1)
				for id := range e.agentStore.documents {
					assert.Equal(t, id, rec.EventData.Parameters["document_id"])
				}
				assert.Equal(t, agentdocs.MimeTypeText, rec.EventData.Parameters["mime_type"])
				assert.Equal(t, int64(len(plantedContent)), rec.EventData.Parameters["size"])
			},
		},
		{
			name:           "rejected document records a 400 fail",
			canCreate:      true,
			content:        "   ",
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, _ *TestEnvironment, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusBadRequest, rec.Error.Code)
			},
		},
		{
			name:           "caller without permission records a 403 fail",
			content:        plantedContent,
			expectedStatus: http.StatusForbidden,
			validateRecord: func(t *testing.T, _ *TestEnvironment, rec *model.AuditRecord) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusForbidden, rec.Error.Code)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(tt.canCreate)
			e.mockAPI.On("HasPermissionTo", testUserID, mock.Anything).Return(false).Maybe()
			records := e.CaptureAuditRecords()

			body, contentType := multipartDocumentBody(t, plantedName, []byte(tt.content))
			recorder := doUploadDocument(e, body, contentType)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())

			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, AuditEventUploadAgentDocument, rec.EventName)
			assert.Equal(t, testUserID, rec.Actor.UserId)
			tt.validateRecord(t, e, rec)

			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), "planted-document-name-sentinel", "audit record must never carry the file name")
			assert.NotContains(t, string(raw), plantedContent, "audit record must never carry document content")
		})
	}
}

func TestCreateAgentWithDocuments(t *testing.T) {
	tests := []struct {
		name string
		// documents returns the request's documents for the seeded store.
		documents      func(t *testing.T, e *TestEnvironment) (request []map[string]any, want []llm.AgentDocument)
		expectedStatus int
		errorContains  string
	}{
		{
			name: "metadata comes from the stored document, not the request",
			documents: func(t *testing.T, e *TestEnvironment) ([]map[string]any, []llm.AgentDocument) {
				doc := seedAgentDocument(t, e, "handbook.md", "hello", testUserID)
				request := []map[string]any{{
					"id": doc.ID, "name": "  Team handbook.md ",
					"mimeType": "application/pdf", "size": 1, "sha256": "forged", "textRunes": 1,
				}}
				doc.Name = "Team handbook.md"
				return request, []llm.AgentDocument{doc}
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "name defaults to the stored file name and order is kept",
			documents: func(t *testing.T, e *TestEnvironment) ([]map[string]any, []llm.AgentDocument) {
				second := seedAgentDocument(t, e, "second.txt", "two", testUserID)
				first := seedAgentDocument(t, e, "first.json", `{"a":1}`, testUserID)
				return []map[string]any{{"id": first.ID}, {"id": second.ID, "name": ""}}, []llm.AgentDocument{first, second}
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "no documents",
			documents: func(*testing.T, *TestEnvironment) ([]map[string]any, []llm.AgentDocument) {
				return nil, []llm.AgentDocument{}
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "someone else's document is rejected",
			documents: func(t *testing.T, e *TestEnvironment) ([]map[string]any, []llm.AgentDocument) {
				doc := seedAgentDocument(t, e, "theirs.md", "secret", otherUserID)
				return []map[string]any{{"id": doc.ID}}, nil
			},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "unknown document",
		},
		{
			name: "missing document is rejected",
			documents: func(*testing.T, *TestEnvironment) ([]map[string]any, []llm.AgentDocument) {
				return []map[string]any{{"id": model.NewId()}}, nil
			},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "unknown document",
		},
		{
			name: "duplicate document",
			documents: func(t *testing.T, e *TestEnvironment) ([]map[string]any, []llm.AgentDocument) {
				doc := seedAgentDocument(t, e, "handbook.md", "hello", testUserID)
				return []map[string]any{{"id": doc.ID}, {"id": doc.ID, "name": "copy.md"}}, nil
			},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "is listed more than once",
		},
		{
			name: "extracted text budget",
			documents: func(t *testing.T, e *TestEnvironment) ([]map[string]any, []llm.AgentDocument) {
				a := seedAgentDocument(t, e, "a.txt", strings.Repeat("a", 60000), testUserID)
				b := seedAgentDocument(t, e, "b.txt", strings.Repeat("b", 60000), testUserID)
				return []map[string]any{{"id": a.ID}, {"id": b.ID}}, nil
			},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "documents exceed the limit of 100000 characters of extracted text (have 120000)",
		},
		{
			name: "too many documents",
			documents: func(t *testing.T, e *TestEnvironment) ([]map[string]any, []llm.AgentDocument) {
				request := make([]map[string]any, 0, agentdocs.MaxDocumentsPerAgent+1)
				for i := range agentdocs.MaxDocumentsPerAgent + 1 {
					doc := seedAgentDocument(t, e, fmt.Sprintf("doc-%d.txt", i), fmt.Sprintf("text %d", i), testUserID)
					request = append(request, map[string]any{"id": doc.ID})
				}
				return request, nil
			},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "at most 20 reference documents",
		},
		{
			name: "name with a path separator",
			documents: func(t *testing.T, e *TestEnvironment) ([]map[string]any, []llm.AgentDocument) {
				doc := seedAgentDocument(t, e, "handbook.md", "hello", testUserID)
				return []map[string]any{{"id": doc.ID, "name": "../handbook.md"}}, nil
			},
			expectedStatus: http.StatusBadRequest,
			errorContains:  "cannot contain /",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			mockLicensed(e.mockAPI)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{UserId: "bot-user-id-created"}, nil).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

			request, want := tt.documents(t, e)
			recorder := doRequest(e.api, http.MethodPost, "/agents", createAgentBody(map[string]any{"documents": request}), testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())

			if tt.errorContains != "" {
				assert.Contains(t, decodeAgentError(t, recorder), tt.errorContains)
				assert.Empty(t, e.agentStore.agents, "a rejected create stores nothing")
				e.mockAPI.AssertNotCalled(t, "CreateBot", mock.Anything)
				return
			}
			var created llm.BotConfig
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &created))
			assert.Equal(t, want, created.Documents)
			assert.Equal(t, want, e.agentStore.agents[created.ID].Documents)
		})
	}
}

// documentsAgent is an agent created by otherUserID that testUserID manages
// as an agent admin. Its current documents are current; earlier documents
// only appear in its version 1.
func seedDocumentsAgent(t *testing.T, e *TestEnvironment, earlier, current []llm.AgentDocument) *llm.BotConfig {
	t.Helper()
	agent := &llm.BotConfig{
		ID: "agent-1", CreatorID: otherUserID, AdminUserIDs: []string{testUserID}, BotUserID: "bot-1",
		DisplayName: "Agent", Name: "my-agent", ServiceID: "svc-1", Documents: earlier,
	}
	e.agentStore.agents[agent.ID] = cloneBotConfig(agent)
	agent.Documents = current
	require.NoError(t, e.agentStore.UpdateAgent(agent, store.AgentVersionMeta{ActorID: otherUserID, Source: store.AgentVersionSourceUpdate}))
	return cloneBotConfig(agent)
}

func TestUpdateAgentDocuments(t *testing.T) {
	type seeded struct {
		attached, earlier, unattached, own llm.AgentDocument
	}

	tests := []struct {
		name           string
		documents      func(s seeded) any
		expectedStatus int
		errorContains  string
		want           func(s seeded) []llm.AgentDocument
	}{
		{
			name:           "keeps a document the agent has, uploaded by someone else",
			documents:      func(s seeded) any { return []AgentDocumentRef{{ID: s.attached.ID, Name: s.attached.Name}} },
			expectedStatus: http.StatusOK,
			want:           func(s seeded) []llm.AgentDocument { return []llm.AgentDocument{s.attached} },
		},
		{
			name:           "brings back a document from an earlier version",
			documents:      func(s seeded) any { return []AgentDocumentRef{{ID: s.earlier.ID}} },
			expectedStatus: http.StatusOK,
			want:           func(s seeded) []llm.AgentDocument { return []llm.AgentDocument{s.earlier} },
		},
		{
			name: "adds the caller's own upload and renames",
			documents: func(s seeded) any {
				return []AgentDocumentRef{{ID: s.own.ID}, {ID: s.attached.ID, Name: "Renamed.md"}}
			},
			expectedStatus: http.StatusOK,
			want: func(s seeded) []llm.AgentDocument {
				renamed := s.attached
				renamed.Name = "Renamed.md"
				return []llm.AgentDocument{s.own, renamed}
			},
		},
		{
			name:           "omitted documents remove them",
			documents:      func(seeded) any { return nil },
			expectedStatus: http.StatusOK,
			want:           func(seeded) []llm.AgentDocument { return []llm.AgentDocument{} },
		},
		{
			name:           "someone else's document the agent never had is rejected",
			documents:      func(s seeded) any { return []AgentDocumentRef{{ID: s.unattached.ID}} },
			expectedStatus: http.StatusBadRequest,
			errorContains:  "unknown document",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			mockLicensed(e.mockAPI)
			e.mockAPI.On("HasPermissionTo", testUserID, mock.Anything).Return(false).Maybe()
			e.mockAPI.On("PatchBot", "bot-1", mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()

			s := seeded{
				attached:   seedAgentDocument(t, e, "attached.md", "attached text", otherUserID),
				earlier:    seedAgentDocument(t, e, "earlier.md", "earlier text", otherUserID),
				unattached: seedAgentDocument(t, e, "unattached.md", "unattached text", otherUserID),
				own:        seedAgentDocument(t, e, "own.txt", "own text", testUserID),
			}
			agent := seedDocumentsAgent(t, e, []llm.AgentDocument{s.earlier}, []llm.AgentDocument{s.attached})
			records := e.CaptureAuditRecords()

			body := updateAgentBodyFromStored(agent, nil)
			if docs := tt.documents(s); docs != nil {
				body["documents"] = docs
			} else {
				delete(body, "documents")
			}
			recorder := doRequest(e.api, http.MethodPut, "/agents/agent-1", body, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())

			if tt.errorContains != "" {
				assert.Contains(t, decodeAgentError(t, recorder), tt.errorContains)
				assert.Equal(t, []llm.AgentDocument{s.attached}, e.agentStore.agents["agent-1"].Documents)
				return
			}
			want := tt.want(s)
			var updated llm.BotConfig
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &updated))
			assert.Equal(t, want, updated.Documents)
			assert.Equal(t, want, e.agentStore.agents["agent-1"].Documents)

			changed := !assert.ObjectsAreEqual([]llm.AgentDocument{s.attached}, want)
			require.Len(t, *records, 1)
			if changed {
				assert.Contains(t, (*records)[0].EventData.Parameters["changed_fields"], "documents")
				versions, err := e.agentStore.ListAgentVersions("agent-1")
				require.NoError(t, err)
				assert.Contains(t, versions[0].ChangedFields, "documents")
			} else {
				assert.NotContains(t, (*records)[0].EventData.Parameters["changed_fields"], "documents")
			}
		})
	}
}

func TestRestoreAgentVersionDocuments(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)
	mockLicensed(e.mockAPI)
	e.mockAPI.On("HasPermissionTo", testUserID, mock.Anything).Return(false).Maybe()

	// Version 1 has documents uploaded by the agent's creator; version 2
	// replaced them. The restoring manager uploaded neither.
	handbook := seedAgentDocument(t, e, "handbook.md", "handbook text", otherUserID)
	faq := seedAgentDocument(t, e, "faq.txt", "faq text", otherUserID)
	replacement := seedAgentDocument(t, e, "new.md", "new text", otherUserID)
	seedDocumentsAgent(t, e, []llm.AgentDocument{handbook, faq}, []llm.AgentDocument{replacement})

	recorder := doRequest(e.api, http.MethodPost, "/agents/agent-1/versions/1/restore", nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())

	var restored llm.BotConfig
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &restored))
	assert.Equal(t, []llm.AgentDocument{handbook, faq}, restored.Documents)
	assert.Equal(t, []llm.AgentDocument{handbook, faq}, e.agentStore.agents["agent-1"].Documents)

	versions, err := e.agentStore.ListAgentVersions("agent-1")
	require.NoError(t, err)
	require.Len(t, versions, 3)
	assert.Equal(t, store.AgentVersionSourceRestore, versions[0].Source)
	assert.Equal(t, []string{"documents"}, versions[0].ChangedFields)
}

func TestAgentDocumentDownloadAndText(t *testing.T) {
	type seeded struct {
		current, earlier, unreferenced, otherAgents llm.AgentDocument
	}
	const earlierName = "Old notes.txt"

	tests := []struct {
		name           string
		path           func(s seeded) string
		expectedStatus int
		want           func(s seeded) (doc llm.AgentDocument, disposition string)
	}{
		{
			name:           "current document",
			path:           func(s seeded) string { return "/agents/agent-1/documents/" + s.current.ID },
			expectedStatus: http.StatusOK,
			want: func(s seeded) (llm.AgentDocument, string) {
				return s.current, `attachment; filename="_ber handbook.md"; filename*=UTF-8''%C3%9Cber%20handbook.md`
			},
		},
		{
			name:           "document of an earlier version uses that version's name",
			path:           func(s seeded) string { return "/agents/agent-1/documents/" + s.earlier.ID },
			expectedStatus: http.StatusOK,
			want: func(s seeded) (llm.AgentDocument, string) {
				return s.earlier, `attachment; filename="Old notes.txt"`
			},
		},
		{
			name:           "the caller's own upload the agent does not reference",
			path:           func(s seeded) string { return "/agents/agent-1/documents/" + s.unreferenced.ID },
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "another agent's document",
			path:           func(s seeded) string { return "/agents/agent-1/documents/" + s.otherAgents.ID },
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "unknown document",
			path:           func(seeded) string { return "/agents/agent-1/documents/" + model.NewId() },
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "unknown agent",
			path:           func(s seeded) string { return "/agents/missing/documents/" + s.current.ID },
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "agent the caller cannot manage",
			path:           func(s seeded) string { return "/agents/agent-2/documents/" + s.otherAgents.ID },
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		for _, route := range []string{"download", "text"} {
			t.Run(tt.name+"/"+route, func(t *testing.T) {
				e := setupAgentTestEnvironment(t)
				defer e.Cleanup(t)
				mockLicensed(e.mockAPI)
				e.mockAPI.On("HasPermissionTo", testUserID, mock.Anything).Return(false).Maybe()

				s := seeded{
					current:      seedAgentDocument(t, e, "Über handbook.md", "current text", otherUserID),
					earlier:      seedAgentDocument(t, e, "notes.txt", "earlier text", otherUserID),
					unreferenced: seedAgentDocument(t, e, "mine.txt", "my text", testUserID),
					otherAgents:  seedAgentDocument(t, e, "secret.txt", "planted other agent's document text", otherUserID),
				}
				renamed := s.earlier
				renamed.Name = earlierName
				seedDocumentsAgent(t, e, []llm.AgentDocument{renamed}, []llm.AgentDocument{s.current})
				e.agentStore.agents["agent-2"] = &llm.BotConfig{
					ID: "agent-2", CreatorID: otherUserID, Name: "other-agent", Documents: []llm.AgentDocument{s.otherAgents},
				}
				records := e.CaptureAuditRecords()

				path := tt.path(s)
				if route == "text" {
					path += "/text"
				}
				recorder := doRequest(e.api, http.MethodGet, path, nil, testUserID)
				require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())
				assert.Empty(t, *records, "document reads are not audited")

				if tt.want == nil {
					assert.NotContains(t, recorder.Body.String(), "planted other agent's document text")
					return
				}
				doc, disposition := tt.want(s)
				stored := e.agentStore.documents[doc.ID]
				if route == "download" {
					assert.Equal(t, stored.Content, recorder.Body.Bytes())
					assert.Equal(t, doc.MimeType, recorder.Header().Get("Content-Type"))
					assert.Equal(t, disposition, recorder.Header().Get("Content-Disposition"))
					assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
					return
				}
				var resp agentDocumentTextResponse
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
				assert.Equal(t, agentDocumentTextResponse{ID: doc.ID, Text: stored.ExtractedText, TextRunes: stored.TextRunes}, resp)
			})
		}
	}
}

func TestAttachmentContentDisposition(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{name: "plain ASCII", filename: "handbook.pdf", want: `attachment; filename="handbook.pdf"`},
		{name: "quotes and backslashes", filename: `a"b\c.txt`, want: `attachment; filename="a_b_c.txt"; filename*=UTF-8''a%22b%5Cc.txt`},
		{name: "non-ASCII", filename: "résumé.md", want: `attachment; filename="r_sum_.md"; filename*=UTF-8''r%C3%A9sum%C3%A9.md`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, attachmentContentDisposition(tt.filename))
		})
	}
}

func TestAgentDocumentsHiddenFromNonManagers(t *testing.T) {
	e := setupAgentTestEnvironment(t)
	defer e.Cleanup(t)
	e.mockAPI.On("HasPermissionTo", testUserID, mock.Anything).Return(false)

	cfg := &llm.BotConfig{ID: "agent-1", CreatorID: otherUserID, Documents: []llm.AgentDocument{{ID: "doc-1", Name: "secret.pdf"}}}
	assert.Equal(t, []llm.AgentDocument{}, sanitizeAgentForUser(e.api.pluginAPI, cfg, testUserID).Documents)
	assert.Equal(t, cfg.Documents, sanitizeAgentForUser(e.api.pluginAPI, cfg, otherUserID).Documents)
}

func exportedDocument(name, mimeType, content string) agentexport.AgentDocument {
	return agentexport.AgentDocument{
		Name: name, MimeType: mimeType, Size: int64(len(content)),
		SHA256: sha256Hex([]byte(content)), Content: []byte(content),
	}
}

func TestExportAgentDocuments(t *testing.T) {
	e := setupTransferTestEnvironment(t)
	defer e.Cleanup(t)
	e.mockAPI.On("HasPermissionTo", testUserID, mock.Anything).Return(false).Maybe()

	const content = "region,total\nnorth,3\n"
	doc := seedAgentDocument(t, e, "sales.csv", content, testUserID)
	agent := fullyConfiguredAgent()
	doc.Name = "Sales figures.csv"
	agent.Documents = []llm.AgentDocument{doc}
	e.agentStore.agents[agent.ID] = agent

	recorder := doRequest(e.api, http.MethodGet, "/agents/agent-1/export", nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"content": "`+base64.StdEncoding.EncodeToString([]byte(content))+`"`)

	var exported agentexport.Document
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &exported))
	assert.Equal(t, agentexport.SchemaVersion, exported.SchemaVersion)
	assert.Equal(t, []agentexport.AgentDocument{exportedDocument("Sales figures.csv", agentdocs.MimeTypeCSV, content)}, exported.Agent.Documents)
	assert.NotContains(t, recorder.Body.String(), doc.ID, "document ids are local to this server")

	_, err := exported.Normalize()
	assert.NoError(t, err, "an export can be imported again")
}

func TestPreviewAgentImportDocuments(t *testing.T) {
	e := setupTransferTestEnvironment(t)
	defer e.Cleanup(t)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false)

	// Larger than the 2 MiB agent request limit once base64-encoded.
	large := strings.Repeat("planted-preview-content ", 120000)
	doc := exportDocument()
	doc.Agent.Documents = []agentexport.AgentDocument{
		exportedDocument("guide.md", agentdocs.MimeTypeMarkdown, "# Guide"),
		exportedDocument("large.txt", agentdocs.MimeTypeText, large),
	}

	recorder := doRequest(e.api, http.MethodPost, "/agents/import/preview", map[string]any{"document": doc}, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())
	assert.Less(t, recorder.Body.Len(), 64<<10, "the preview does not echo document content")

	var resp importAgentPreviewResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	assert.Equal(t, []importPreviewDocument{
		{Name: "guide.md", MimeType: agentdocs.MimeTypeMarkdown, Size: 7},
		{Name: "large.txt", MimeType: agentdocs.MimeTypeText, Size: int64(len(large))},
	}, resp.Documents)
	require.Len(t, resp.Document.Agent.Documents, 2)
	for i, d := range resp.Document.Agent.Documents {
		assert.Nil(t, d.Content)
		assert.Equal(t, doc.Agent.Documents[i].SHA256, d.SHA256)
	}
}

func TestImportAgentDocuments(t *testing.T) {
	const existingText = "existing document text"
	v1Document := func() agentexport.Document {
		doc := exportDocument()
		doc.SchemaVersion = 1
		return doc
	}
	withDocuments := func(docs ...agentexport.AgentDocument) agentexport.Document {
		doc := exportDocument()
		doc.Agent.Documents = docs
		return doc
	}
	tampered := exportedDocument("guide.md", agentdocs.MimeTypeMarkdown, "# Guide")
	tampered.Content = []byte("# Tampered")
	tampered.Size = int64(len(tampered.Content))

	tests := []struct {
		name           string
		mode           string
		document       agentexport.Document
		expectedStatus int
		errorContains  string
		// wantTexts are the extracted texts of the resulting agent's documents, by name.
		wantNames []string
		wantTexts []string
	}{
		{
			name:           "schema version 1 creates an agent without documents",
			mode:           "create",
			document:       v1Document(),
			expectedStatus: http.StatusCreated,
			wantNames:      []string{},
			wantTexts:      []string{},
		},
		{
			name:           "schema version 1 update keeps the agent's documents",
			mode:           "update",
			document:       v1Document(),
			expectedStatus: http.StatusOK,
			wantNames:      []string{"existing.md"},
			wantTexts:      []string{existingText},
		},
		{
			name: "create stores the documents for the importer",
			mode: "create",
			document: withDocuments(
				exportedDocument("guide.md", agentdocs.MimeTypeMarkdown, "# Guide\r\n"),
				exportedDocument("data.json", agentdocs.MimeTypeJSON, `{"a": 1}`),
			),
			expectedStatus: http.StatusCreated,
			wantNames:      []string{"guide.md", "data.json"},
			wantTexts:      []string{"# Guide", `{"a": 1}`},
		},
		{
			name:           "update replaces the agent's documents",
			mode:           "update",
			document:       withDocuments(exportedDocument("guide.md", agentdocs.MimeTypeMarkdown, "# Guide")),
			expectedStatus: http.StatusOK,
			wantNames:      []string{"guide.md"},
			wantTexts:      []string{"# Guide"},
		},
		{
			name: "documents with identical content are referenced once, under the first name",
			mode: "update",
			document: withDocuments(
				exportedDocument("first.txt", agentdocs.MimeTypeText, "Same text"),
				exportedDocument("second.txt", agentdocs.MimeTypeText, "Same text"),
				exportedDocument("same-as-markdown.md", agentdocs.MimeTypeMarkdown, "Same text"),
			),
			expectedStatus: http.StatusOK,
			wantNames:      []string{"first.txt", "same-as-markdown.md"},
			wantTexts:      []string{"Same text", "Same text"},
		},
		{
			name:           "update with an empty document list removes the agent's documents",
			mode:           "update",
			document:       withDocuments(),
			expectedStatus: http.StatusOK,
			wantNames:      []string{},
			wantTexts:      []string{},
		},
		{
			name:           "checksum mismatch",
			mode:           "create",
			document:       withDocuments(tampered),
			expectedStatus: http.StatusBadRequest,
			errorContains:  `reference document "guide.md" does not match its sha256 checksum`,
		},
		{
			name: "size mismatch",
			mode: "update",
			document: func() agentexport.Document {
				d := exportedDocument("guide.md", agentdocs.MimeTypeMarkdown, "# Guide")
				d.Size++
				return withDocuments(d)
			}(),
			expectedStatus: http.StatusBadRequest,
			errorContains:  "is 7 bytes but its size says 8",
		},
		{
			name:           "unsupported type",
			mode:           "create",
			document:       withDocuments(exportedDocument("page.html", "text/html", "<p>hi</p>")),
			expectedStatus: http.StatusBadRequest,
			errorContains:  `unsupported type "text/html"`,
		},
		{
			name:           "document without text",
			mode:           "create",
			document:       withDocuments(exportedDocument("blank.txt", agentdocs.MimeTypeText, "   ")),
			expectedStatus: http.StatusBadRequest,
			errorContains:  `no extractable text found in "blank.txt"`,
		},
		{
			name: "extracted text budget",
			mode: "update",
			document: withDocuments(
				exportedDocument("a.txt", agentdocs.MimeTypeText, strings.Repeat("a", 60000)),
				exportedDocument("b.txt", agentdocs.MimeTypeText, strings.Repeat("b", 60000)),
			),
			expectedStatus: http.StatusBadRequest,
			errorContains:  "documents exceed the limit of 100000 characters of extracted text (have 120000)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupTransferTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("HasPermissionTo", testUserID, mock.Anything).Return(false).Maybe()
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{UserId: "imported-bot-user"}, nil).Maybe()

			existing := seedAgentDocument(t, e, "existing.md", existingText, testUserID)
			agent := fullyConfiguredAgent()
			agent.Documents = []llm.AgentDocument{existing}
			e.agentStore.agents[agent.ID] = agent

			body := map[string]any{"document": tt.document, "mode": tt.mode}
			if tt.mode == "create" {
				body["username"], body["displayName"], body["serviceID"] = "imported-agent", "Imported Agent", "svc-1"
			} else {
				body["agentID"] = agent.ID
			}
			recorder := doRequest(e.api, http.MethodPost, "/agents/import", body, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())

			if tt.errorContains != "" {
				assert.Contains(t, decodeAgentError(t, recorder), tt.errorContains)
				assert.Len(t, e.agentStore.agents, 1, "a rejected import creates no agent")
				assert.Equal(t, []llm.AgentDocument{existing}, e.agentStore.agents[agent.ID].Documents)
				assert.Len(t, e.agentStore.documents, 1, "a rejected import stores no documents")
				return
			}

			var result llm.BotConfig
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
			stored := e.agentStore.agents[result.ID]
			require.NotNil(t, stored)
			assert.Equal(t, result.Documents, stored.Documents)
			names := make([]string, 0, len(stored.Documents))
			texts := make([]string, 0, len(stored.Documents))
			for _, ref := range stored.Documents {
				blob := e.agentStore.documents[ref.ID]
				require.NotNil(t, blob)
				assert.Equal(t, testUserID, blob.CreatedBy)
				assert.Equal(t, blob.SHA256, ref.SHA256)
				names = append(names, ref.Name)
				texts = append(texts, blob.ExtractedText)
			}
			assert.Equal(t, tt.wantNames, names)
			assert.Equal(t, tt.wantTexts, texts)
		})
	}
}

// Two managers uploading the same file give an agent two documents with
// identical bytes; on import both resolve to one document of the importer.
func TestExportImportAgentWithIdenticalDocuments(t *testing.T) {
	for _, mode := range []string{"create", "update"} {
		t.Run(mode, func(t *testing.T) {
			e := setupTransferTestEnvironment(t)
			defer e.Cleanup(t)
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
			e.mockAPI.On("HasPermissionTo", testUserID, mock.Anything).Return(false).Maybe()
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{UserId: "imported-bot-user"}, nil).Maybe()

			const content = "Shared policy text"
			mine := seedAgentDocument(t, e, "policy.txt", content, testUserID)
			theirs := seedAgentDocument(t, e, "policy.txt", content, otherUserID)
			require.NotEqual(t, mine.ID, theirs.ID)
			mine.Name, theirs.Name = "Policy (mine).txt", "Policy (theirs).txt"
			agent := fullyConfiguredAgent()
			agent.Documents = []llm.AgentDocument{theirs, mine}
			e.agentStore.agents[agent.ID] = agent

			recorder := doRequest(e.api, http.MethodGet, "/agents/agent-1/export", nil, testUserID)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())
			var exported agentexport.Document
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &exported))
			require.Len(t, exported.Agent.Documents, 2)

			body := map[string]any{"document": exported, "mode": mode}
			if mode == "create" {
				body["username"], body["displayName"], body["serviceID"] = "imported-agent", "Imported Agent", "svc-1"
			} else {
				body["agentID"] = agent.ID
			}
			recorder = doRequest(e.api, http.MethodPost, "/agents/import", body, testUserID)
			require.Less(t, recorder.Result().StatusCode, 300, recorder.Body.String())

			var result llm.BotConfig
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
			require.Len(t, result.Documents, 1)
			assert.Equal(t, mine.ID, result.Documents[0].ID, "the importer's own copy is reused")
			assert.Equal(t, "Policy (theirs).txt", result.Documents[0].Name, "the first occurrence keeps its name")
			assert.Equal(t, result.Documents, e.agentStore.agents[result.ID].Documents)
		})
	}
}

// Stored documents are never deleted, so an import that is rejected, or a
// file whose schema version predates documents, must not store any.
func TestImportAgentStoresNoDocumentsUnlessImported(t *testing.T) {
	documentFile := func(schemaVersion int) agentexport.Document {
		doc := exportDocument()
		doc.SchemaVersion = schemaVersion
		doc.Agent.Documents = []agentexport.AgentDocument{exportedDocument("guide.md", agentdocs.MimeTypeMarkdown, "# Guide")}
		return doc
	}

	tests := []struct {
		name           string
		mode           string
		schemaVersion  int
		username       string
		serviceID      string
		canCreate      bool
		level          *enterprise.Level
		mutateAgent    func(agent *llm.BotConfig)
		expectedStatus int
		errorContains  string
	}{
		{name: "create without permission to create agents", mode: "create", expectedStatus: http.StatusForbidden, errorContains: "permission to create agents"},
		{name: "create beyond the agent quota", mode: "create", canCreate: true, level: new(enterprise.LevelUnlicensed), expectedStatus: http.StatusForbidden},
		{name: "create with an invalid username", mode: "create", canCreate: true, username: "Bad Name", expectedStatus: http.StatusBadRequest, errorContains: "invalid username"},
		{name: "create with a taken username", mode: "create", canCreate: true, username: takenUsername, expectedStatus: http.StatusConflict, errorContains: "already taken"},
		{name: "create with an unknown service", mode: "create", canCreate: true, serviceID: "missing-service", expectedStatus: http.StatusBadRequest},
		{name: "create on a service the license does not activate", mode: "create", canCreate: true, serviceID: "svc-2", level: new(enterprise.LevelProfessional), expectedStatus: http.StatusForbidden},
		{name: "update of an agent the user cannot manage", mode: "update", mutateAgent: func(a *llm.BotConfig) { a.CreatorID = otherUserID }, expectedStatus: http.StatusForbidden},
		{name: "update of an agent whose service was removed", mode: "update", mutateAgent: func(a *llm.BotConfig) { a.ServiceID = "missing-service" }, expectedStatus: http.StatusBadRequest},
		{name: "update of an agent whose configuration is invalid", mode: "update", mutateAgent: func(a *llm.BotConfig) { a.ChannelIDs = []string{"bad\x00id"} }, expectedStatus: http.StatusBadRequest, errorContains: "channelIDs[0]"},
		{name: "schema version 1 create ignores documents", mode: "create", schemaVersion: 1, canCreate: true, expectedStatus: http.StatusCreated},
		{name: "schema version 1 update ignores documents", mode: "update", schemaVersion: 1, expectedStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupTransferTestEnvironment(t)
			defer e.Cleanup(t)
			if tt.level != nil {
				e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
				e.OverrideLicense(enterprisetest.LicenseFor(*tt.level))
			}
			e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(tt.canCreate)
			e.mockAPI.On("HasPermissionTo", testUserID, mock.Anything).Return(false).Maybe()
			e.mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(&model.Bot{UserId: "imported-bot-user"}, nil).Maybe()
			cfgStore := e.api.configStore.(*mockConfigStore)
			cfgStore.cfg.Services = append(cfgStore.cfg.Services, llm.ServiceConfig{ID: "svc-2", Name: "Second", Type: "openai", APIKey: "test-key"})

			agent := fullyConfiguredAgent()
			if tt.mutateAgent != nil {
				tt.mutateAgent(agent)
			}
			e.agentStore.agents[agent.ID] = agent
			agentBefore := *agent

			schemaVersion := cmp.Or(tt.schemaVersion, agentexport.DocumentsSchemaVersion)
			body := map[string]any{"document": documentFile(schemaVersion), "mode": tt.mode}
			if tt.mode == "create" {
				body["username"] = cmp.Or(tt.username, "imported-agent")
				body["displayName"] = "Imported Agent"
				body["serviceID"] = cmp.Or(tt.serviceID, "svc-1")
			} else {
				body["agentID"] = agent.ID
			}
			recorder := doRequest(e.api, http.MethodPost, "/agents/import", body, testUserID)
			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode, recorder.Body.String())
			if tt.errorContains != "" {
				assert.Contains(t, decodeAgentError(t, recorder), tt.errorContains)
			}

			assert.Empty(t, e.agentStore.documents, "no document is stored")
			if recorder.Result().StatusCode >= 300 {
				e.mockAPI.AssertNotCalled(t, "CreateBot", mock.Anything)
				assert.Len(t, e.agentStore.agents, 1, "a rejected import creates no agent")
				assert.Equal(t, agentBefore, *e.agentStore.agents[agent.ID], "a rejected import changes no agent")
				return
			}
			var result llm.BotConfig
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
			assert.Empty(t, result.Documents)
		})
	}
}

func TestAuditImportAgentDocuments(t *testing.T) {
	const plantedContent = "planted-imported-document-content"
	e := setupTransferTestEnvironment(t)
	defer e.Cleanup(t)
	e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageOwnAgent).Return(true)
	e.mockAPI.On("HasPermissionTo", testUserID, mock.Anything).Return(false).Maybe()
	e.agentStore.agents["agent-1"] = fullyConfiguredAgent()
	records := e.CaptureAuditRecords()

	doc := exportDocument()
	doc.Agent.Documents = []agentexport.AgentDocument{exportedDocument("planted-imported-name.md", agentdocs.MimeTypeMarkdown, plantedContent)}
	recorder := doRequest(e.api, http.MethodPost, "/agents/import", map[string]any{"document": doc, "mode": "update", "agentID": "agent-1"}, testUserID)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())

	require.Len(t, *records, 1)
	assert.Equal(t, "agent-1", (*records)[0].EventData.Parameters[audit.KeyAgentID])
	assert.Contains(t, (*records)[0].EventData.Parameters["changed_fields"], "documents")
	raw, err := json.Marshal((*records)[0])
	require.NoError(t, err)
	for _, planted := range []string{plantedContent, base64.StdEncoding.EncodeToString([]byte(plantedContent)), "planted-imported-name"} {
		assert.NotContains(t, string(raw), planted)
	}
}

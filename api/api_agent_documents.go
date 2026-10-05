// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/agentdocs"
	"github.com/mattermost/mattermost-plugin-agents/v2/agentexport"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
)

// maxAgentDocumentUploadBytes caps the multipart body of a document upload:
// the largest document plus room for the multipart framing.
const maxAgentDocumentUploadBytes = agentdocs.MaxDocumentBytes + 1<<20

// AgentDocumentRef is a reference document in agent create and update
// requests. Only ID and Name are read; everything else about the document is
// taken from the stored document.
type AgentDocumentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// agentDocumentTextResponse is the body of GET /agents/:agentid/documents/:documentid/text.
type agentDocumentTextResponse struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	TextRunes int    `json:"textRunes"`
}

func documentRefs(docs []llm.AgentDocument) []AgentDocumentRef {
	refs := make([]AgentDocumentRef, 0, len(docs))
	for _, doc := range docs {
		refs = append(refs, AgentDocumentRef{ID: doc.ID, Name: doc.Name})
	}
	return refs
}

func agentDocumentFromStored(doc *store.AgentDocument, name string) llm.AgentDocument {
	return llm.AgentDocument{
		ID:        doc.ID,
		Name:      name,
		MimeType:  doc.MimeType,
		Size:      doc.Size,
		SHA256:    doc.SHA256,
		TextRunes: doc.TextRunes,
	}
}

// checkAgentDocumentLimits enforces the per-agent document count, size and
// extracted-text budgets.
func checkAgentDocumentLimits(docs []llm.AgentDocument) error {
	if len(docs) > agentdocs.MaxDocumentsPerAgent {
		return fmt.Errorf("an agent can have at most %d reference documents (have %d)", agentdocs.MaxDocumentsPerAgent, len(docs))
	}
	var size int64
	runes := 0
	for _, doc := range docs {
		size += doc.Size
		runes += doc.TextRunes
	}
	if size > agentdocs.MaxTotalBytesPerAgent {
		return fmt.Errorf("documents exceed the limit of %d bytes in total (have %d)", agentdocs.MaxTotalBytesPerAgent, size)
	}
	if runes > agentdocs.MaxTotalTextRunes {
		return fmt.Errorf("documents exceed the limit of %d characters of extracted text (have %d)", agentdocs.MaxTotalTextRunes, runes)
	}
	return nil
}

// resolveAgentDocuments turns the document references of a create (agentID
// "") or update request into the documents the agent will store. Every
// document must exist and be either uploaded by userID or already referenced
// by the agent (its current configuration or any of its versions); its
// metadata comes from the stored document, and its name from the request
// (default: the stored file name). It writes the abort response and returns
// false when a reference or the per-agent limits are invalid.
func (a *API) resolveAgentDocuments(c *gin.Context, userID, agentID string, refs []AgentDocumentRef) ([]llm.AgentDocument, bool) {
	docs := make([]llm.AgentDocument, 0, len(refs))
	if len(refs) == 0 {
		return docs, true
	}
	if len(refs) > agentdocs.MaxDocumentsPerAgent {
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("an agent can have at most %d reference documents (have %d)", agentdocs.MaxDocumentsPerAgent, len(refs)))
		return nil, false
	}

	ids := make([]string, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if seen[ref.ID] {
			abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("document %q is listed more than once", audit.TruncateID(ref.ID)))
			return nil, false
		}
		seen[ref.ID] = true
		ids = append(ids, ref.ID)
	}

	stored, err := a.agentStore.GetAgentDocumentInfos(ids)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to load agent documents: %w", err))
		return nil, false
	}
	referenced := make(map[string]bool)
	if agentID != "" {
		existing, listErr := a.agentStore.ListAgentDocumentReferences(agentID)
		if listErr != nil {
			abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to load agent documents: %w", listErr))
			return nil, false
		}
		for _, doc := range existing {
			referenced[doc.ID] = true
		}
	}

	for _, ref := range refs {
		doc := stored[ref.ID]
		if doc == nil || (!referenced[ref.ID] && doc.CreatedBy != userID) {
			abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("unknown document %q", audit.TruncateID(ref.ID)))
			return nil, false
		}
		name := ref.Name
		if strings.TrimSpace(name) == "" {
			name = doc.Name
		}
		name, err = agentdocs.NormalizeName(name)
		if err != nil {
			abortAgentRequest(c, http.StatusBadRequest, err)
			return nil, false
		}
		docs = append(docs, agentDocumentFromStored(doc, name))
	}

	if err := checkAgentDocumentLimits(docs); err != nil {
		abortAgentRequest(c, http.StatusBadRequest, err)
		return nil, false
	}
	return docs, true
}

// handleUploadAgentDocument handles POST /agents/documents (multipart field
// "file"): validates the file, extracts its text and stores it for the
// caller to reference from an agent they create or manage. It is not
// attached to any agent until an agent save references it.
func (a *API) handleUploadAgentDocument(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	allowed, err := a.canCreateOrManageAnyAgent(userID)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, err)
		return
	}
	if !allowed {
		abortAgentRequest(c, http.StatusForbidden, errors.New("user does not have permission to upload agent documents"))
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAgentDocumentUploadBytes)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			abortAgentRequest(c, http.StatusRequestEntityTooLarge, fmt.Errorf("document is too large (max %d MiB)", agentdocs.MaxDocumentBytes>>20))
			return
		}
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("missing or invalid document file: %w", err))
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, agentdocs.MaxDocumentBytes+1))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			abortAgentRequest(c, http.StatusRequestEntityTooLarge, fmt.Errorf("document is too large (max %d MiB)", agentdocs.MaxDocumentBytes>>20))
			return
		}
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("failed to read document: %w", err))
		return
	}
	if len(data) > agentdocs.MaxDocumentBytes {
		abortAgentRequest(c, http.StatusRequestEntityTooLarge, fmt.Errorf("document is too large (max %d MiB)", agentdocs.MaxDocumentBytes>>20))
		return
	}

	doc, ok := extractAgentDocument(c, header.Filename, "", data, userID)
	if !ok {
		return
	}
	if err := a.agentStore.SaveAgentDocument(doc); err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to save document: %w", err))
		return
	}

	// The file name is user content and never recorded.
	audit.AddParam(auditRec(c), "document_id", doc.ID)
	audit.AddParam(auditRec(c), "mime_type", doc.MimeType)
	audit.AddParam(auditRec(c), "size", doc.Size)

	c.JSON(http.StatusCreated, agentDocumentFromStored(doc, doc.Name))
}

// extractAgentDocument validates a document's name and type (mimeType "" =
// decided by the name's extension) and extracts its text, returning the
// document to store for userID. It writes the abort response and returns
// false when the document is not acceptable.
func extractAgentDocument(c *gin.Context, rawName, mimeType string, data []byte, userID string) (*store.AgentDocument, bool) {
	name, err := agentdocs.NormalizeName(rawName)
	if err != nil {
		abortAgentRequest(c, http.StatusBadRequest, err)
		return nil, false
	}
	if mimeType == "" {
		mimeType, err = agentdocs.MimeTypeForName(name)
		if err != nil {
			abortAgentRequest(c, http.StatusBadRequest, err)
			return nil, false
		}
	}
	text, err := agentdocs.Extract(c.Request.Context(), name, mimeType, data)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, agentdocs.ErrInvalidDocument) {
			status = http.StatusBadRequest
		}
		abortAgentRequest(c, status, err)
		return nil, false
	}
	return &store.AgentDocument{
		Name:          name,
		MimeType:      mimeType,
		Content:       data,
		ExtractedText: text,
		CreatedBy:     userID,
	}, true
}

// storeImportedDocuments extracts and stores the documents of an import
// document for userID and returns the references to save on the agent, in
// document order. The extracted-text budget is checked before anything is
// stored. It writes the abort response and returns false on failure.
func (a *API) storeImportedDocuments(c *gin.Context, userID string, docs []agentexport.AgentDocument) ([]AgentDocumentRef, bool) {
	extracted := make([]*store.AgentDocument, 0, len(docs))
	budget := make([]llm.AgentDocument, 0, len(docs))
	for _, doc := range docs {
		stored, ok := extractAgentDocument(c, doc.Name, doc.MimeType, doc.Content, userID)
		if !ok {
			return nil, false
		}
		extracted = append(extracted, stored)
		budget = append(budget, llm.AgentDocument{Size: int64(len(stored.Content)), TextRunes: utf8.RuneCountInString(stored.ExtractedText)})
	}
	if err := checkAgentDocumentLimits(budget); err != nil {
		abortAgentRequest(c, http.StatusBadRequest, err)
		return nil, false
	}

	refs := make([]AgentDocumentRef, 0, len(extracted))
	for _, doc := range extracted {
		if err := a.agentStore.SaveAgentDocument(doc); err != nil {
			abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to save document: %w", err))
			return nil, false
		}
		refs = append(refs, AgentDocumentRef{ID: doc.ID, Name: doc.Name})
	}
	return refs, true
}

// exportAgentDocuments loads the content of cfg's documents for an export.
// It writes the abort response and returns false on failure.
func (a *API) exportAgentDocuments(c *gin.Context, cfg *llm.BotConfig) ([]agentexport.AgentDocument, bool) {
	docs := make([]agentexport.AgentDocument, 0, len(cfg.Documents))
	for _, ref := range cfg.Documents {
		stored, err := a.agentStore.GetAgentDocument(ref.ID)
		if err != nil {
			abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to load document %q: %w", ref.ID, err))
			return nil, false
		}
		if stored == nil {
			abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("document %q of agent %q is missing", ref.ID, cfg.ID))
			return nil, false
		}
		docs = append(docs, agentexport.AgentDocument{
			Name:     ref.Name,
			MimeType: stored.MimeType,
			Size:     stored.Size,
			SHA256:   stored.SHA256,
			Content:  stored.Content,
		})
	}
	return docs, true
}

// loadManageableAgentDocument loads the :documentid document of the
// :agentid agent for a caller who may manage that agent, aborting with
// 404/403. The document must be referenced by the agent's current
// configuration or one of its versions. It returns the stored document and
// the name the agent most recently gave it.
func (a *API) loadManageableAgentDocument(c *gin.Context) (*store.AgentDocument, string, bool) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")
	documentID := c.Param("documentid")

	if _, ok := a.loadManageableAgent(c, agentID, userID, "not authorized to view this agent's documents", nil); !ok {
		return nil, "", false
	}

	refs, err := a.agentStore.ListAgentDocumentReferences(agentID)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to load agent documents: %w", err))
		return nil, "", false
	}
	name := ""
	found := false
	for _, ref := range refs {
		if ref.ID == documentID {
			name, found = ref.Name, true
			break
		}
	}
	if !found {
		abortAgentRequest(c, http.StatusNotFound, errors.New("this agent has no such document"))
		return nil, "", false
	}

	doc, err := a.agentStore.GetAgentDocument(documentID)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to load document: %w", err))
		return nil, "", false
	}
	if doc == nil {
		abortAgentRequest(c, http.StatusNotFound, errors.New("this agent has no such document"))
		return nil, "", false
	}
	if name == "" {
		name = doc.Name
	}
	return doc, name, true
}

// handleDownloadAgentDocument handles GET /agents/:agentid/documents/:documentid:
// the document's original bytes as an attachment.
func (a *API) handleDownloadAgentDocument(c *gin.Context) {
	doc, name, ok := a.loadManageableAgentDocument(c)
	if !ok {
		return
	}
	c.Header("Content-Disposition", attachmentContentDisposition(name))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Length", strconv.FormatInt(int64(len(doc.Content)), 10))
	c.Data(http.StatusOK, doc.MimeType, doc.Content)
}

// handleGetAgentDocumentText handles GET /agents/:agentid/documents/:documentid/text:
// the text the agent receives from the document.
func (a *API) handleGetAgentDocumentText(c *gin.Context) {
	doc, _, ok := a.loadManageableAgentDocument(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, agentDocumentTextResponse{ID: doc.ID, Text: doc.ExtractedText, TextRunes: doc.TextRunes})
}

// attachmentContentDisposition returns an attachment Content-Disposition for
// name: an ASCII filename parameter for every client, plus the exact name
// RFC 5987-encoded in filename* when it is not plain ASCII.
func attachmentContentDisposition(name string) string {
	ascii := true
	fallback := strings.Map(func(r rune) rune {
		switch {
		case r == '"' || r == '\\':
			ascii = false
			return '_'
		case r < 0x20 || r > 0x7e:
			ascii = false
			return '_'
		default:
			return r
		}
	}, name)
	header := fmt.Sprintf(`attachment; filename="%s"`, fallback)
	if ascii {
		return header
	}
	var encoded strings.Builder
	for _, b := range []byte(name) {
		if isRFC5987AttrChar(b) {
			encoded.WriteByte(b)
		} else {
			fmt.Fprintf(&encoded, "%%%02X", b)
		}
	}
	return header + "; filename*=UTF-8''" + encoded.String()
}

func isRFC5987AttrChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", b) >= 0
}

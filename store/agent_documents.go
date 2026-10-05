// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// AgentDocument is a stored agent reference document. Stored documents are
// immutable and never deleted: agent version snapshots keep referring to them.
type AgentDocument struct {
	ID       string
	SHA256   string
	Name     string
	MimeType string
	Size     int64
	// Content and ExtractedText are only loaded by GetAgentDocument.
	Content       []byte
	ExtractedText string
	TextRunes     int
	CreatedBy     string
	CreateAt      int64
}

type agentDocumentRow struct {
	ID            string `db:"id"`
	SHA256        string `db:"sha256"`
	Name          string `db:"name"`
	MimeType      string `db:"mimetype"`
	Size          int64  `db:"size"`
	Content       []byte `db:"content"`
	ExtractedText string `db:"extractedtext"`
	TextRunes     int    `db:"textrunes"`
	CreatedBy     string `db:"createdby"`
	CreateAt      int64  `db:"createat"`
}

func (r *agentDocumentRow) toAgentDocument() *AgentDocument {
	return &AgentDocument{
		ID:            r.ID,
		SHA256:        r.SHA256,
		Name:          r.Name,
		MimeType:      r.MimeType,
		Size:          r.Size,
		Content:       r.Content,
		ExtractedText: r.ExtractedText,
		TextRunes:     r.TextRunes,
		CreatedBy:     r.CreatedBy,
		CreateAt:      r.CreateAt,
	}
}

const agentDocumentInfoColumns = `ID, SHA256, Name, MimeType, Size, TextRunes, CreatedBy, CreateAt`

// SaveAgentDocument stores doc.Content and doc.ExtractedText as a new
// document and sets doc's ID, SHA256, Size, TextRunes and CreateAt. When
// doc.CreatedBy already stored the same content with the same MIME type,
// doc gets that document's ID and CreateAt instead and nothing is written;
// that document keeps its original Name.
func (s *Store) SaveAgentDocument(doc *AgentDocument) error {
	sum := sha256.Sum256(doc.Content)
	doc.SHA256 = hex.EncodeToString(sum[:])
	doc.Size = int64(len(doc.Content))
	doc.TextRunes = utf8.RuneCountInString(doc.ExtractedText)

	var existing struct {
		ID       string `db:"id"`
		CreateAt int64  `db:"createat"`
	}
	err := s.db.Get(&existing,
		`SELECT ID, CreateAt FROM Agents_AgentDocuments
		WHERE SHA256 = $1 AND CreatedBy = $2 AND MimeType = $3
		ORDER BY CreateAt, ID
		LIMIT 1`,
		doc.SHA256, doc.CreatedBy, doc.MimeType,
	)
	switch {
	case err == nil:
		doc.ID = existing.ID
		doc.CreateAt = existing.CreateAt
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("failed to look up existing agent document: %w", err)
	}

	doc.ID = model.NewId()
	doc.CreateAt = model.GetMillis()
	if _, err := s.db.Exec(
		`INSERT INTO Agents_AgentDocuments (
			ID, SHA256, Name, MimeType, Size, Content, ExtractedText, TextRunes, CreatedBy, CreateAt
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		doc.ID, doc.SHA256, doc.Name, doc.MimeType, doc.Size, doc.Content,
		doc.ExtractedText, doc.TextRunes, doc.CreatedBy, doc.CreateAt,
	); err != nil {
		return fmt.Errorf("failed to save agent document: %w", err)
	}
	return nil
}

// GetAgentDocument returns the document with its content and extracted
// text, or nil, nil when it does not exist.
func (s *Store) GetAgentDocument(id string) (*AgentDocument, error) {
	var row agentDocumentRow
	err := s.db.Get(&row,
		`SELECT `+agentDocumentInfoColumns+`, Content, ExtractedText
		FROM Agents_AgentDocuments WHERE ID = $1`,
		id,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get agent document %q: %w", id, err)
	}
	return row.toAgentDocument(), nil
}

// GetAgentDocumentInfos returns the documents in ids that exist, keyed by
// ID, without their content or extracted text.
func (s *Store) GetAgentDocumentInfos(ids []string) (map[string]*AgentDocument, error) {
	infos := make(map[string]*AgentDocument, len(ids))
	if len(ids) == 0 {
		return infos, nil
	}
	query, args, err := sqlx.In(`SELECT `+agentDocumentInfoColumns+` FROM Agents_AgentDocuments WHERE ID IN (?)`, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to build agent document query: %w", err)
	}
	var rows []agentDocumentRow
	if err := s.db.Select(&rows, s.db.Rebind(query), args...); err != nil {
		return nil, fmt.Errorf("failed to get agent documents: %w", err)
	}
	for i := range rows {
		infos[rows[i].ID] = rows[i].toAgentDocument()
	}
	return infos, nil
}

// GetAgentDocumentTexts returns the extracted text of the documents in ids
// that exist, keyed by ID.
func (s *Store) GetAgentDocumentTexts(ids []string) (map[string]string, error) {
	texts := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return texts, nil
	}
	query, args, err := sqlx.In(`SELECT ID, ExtractedText FROM Agents_AgentDocuments WHERE ID IN (?)`, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to build agent document text query: %w", err)
	}
	var rows []struct {
		ID            string `db:"id"`
		ExtractedText string `db:"extractedtext"`
	}
	if err := s.db.Select(&rows, s.db.Rebind(query), args...); err != nil {
		return nil, fmt.Errorf("failed to get agent document texts: %w", err)
	}
	for _, row := range rows {
		texts[row.ID] = row.ExtractedText
	}
	return texts, nil
}

// ListAgentDocumentReferences returns every document reference agentID has
// had: its current references first, then those of its versions, newest
// version first. A document can appear more than once.
func (s *Store) ListAgentDocumentReferences(agentID string) ([]llm.AgentDocument, error) {
	var lists []string
	err := s.db.Select(&lists,
		`SELECT refs FROM (
			SELECT Documents AS refs, 0 AS ord, 0 AS version
			FROM Agents_UserAgents WHERE ID = $1
			UNION ALL
			SELECT COALESCE(Config::jsonb -> 'documents', '[]'::jsonb)::text, 1, Version
			FROM Agents_AgentVersions WHERE AgentID = $1
		) refs
		ORDER BY ord, version DESC`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list document references of agent %q: %w", agentID, err)
	}

	var refs []llm.AgentDocument
	for _, raw := range lists {
		if raw == "" || raw == "null" {
			continue
		}
		var docs []llm.AgentDocument
		if err := json.Unmarshal([]byte(raw), &docs); err != nil {
			return nil, fmt.Errorf("failed to parse document references of agent %q: %w", agentID, err)
		}
		refs = append(refs, docs...)
	}
	return refs, nil
}

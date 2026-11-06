// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

func versionedBlobKey(id string, version, n int) string {
	return fmt.Sprintf("%s%s_v%d_%d", keyPrefixBlob, id, version, n)
}

func legacyBlobKey(id string, n int) string {
	return fmt.Sprintf("%s%s_%d", keyPrefixBlob, id, n)
}

func blobKeyFor(rec VersionRecord, id string, n int) string {
	if rec.LegacyUnversionedBlobs {
		return legacyBlobKey(id, n)
	}
	return versionedBlobKey(id, rec.Version, n)
}

func (s *Store) writeVersionBlobs(id string, version int, chunks [][]byte) error {
	for i, chunk := range chunks {
		if err := s.kv.KVSet(versionedBlobKey(id, version, i), chunk); err != nil {
			s.deleteVersionBlobs(VersionRecord{Version: version, ChunkCount: i, LegacyUnversionedBlobs: false}, id)
			return err
		}
	}
	return nil
}

func (s *Store) deleteVersionBlobs(rec VersionRecord, id string) {
	_ = s.deleteVersionBlobKeys(rec, id)
}

func (s *Store) deleteVersionBlobKeys(rec VersionRecord, id string) error {
	var first error
	for i := 0; i < rec.ChunkCount; i++ {
		if err := s.kv.KVDelete(blobKeyFor(rec, id, i)); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *Store) deleteAllVersionBlobs(hist *VersionHistory, id string) {
	_ = s.deleteAllVersionBlobKeys(hist, id)
}

func (s *Store) deleteAllVersionBlobKeys(hist *VersionHistory, id string) error {
	var first error
	for _, rec := range append(append([]VersionRecord{}, hist.Versions...), hist.PendingDelete...) {
		if err := s.deleteVersionBlobKeys(rec, id); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ReadContentAt reassembles HTML for a specific version. version 0 means head.
func (s *Store) ReadContentAt(entry *Entry, version int) ([]byte, error) {
	if entry == nil {
		return nil, ErrInvalidInput
	}
	hist, err := s.ensureHistory(entry)
	if err != nil {
		return nil, err
	}
	target := entry.Version
	if version > 0 {
		target = version
	}
	rec, ok := findVersion(hist, target)
	if !ok {
		return nil, ErrNotFound
	}
	return s.readVersionContent(entry.ID, rec)
}

// ReadContent reassembles and gunzips the current head HTML.
func (s *Store) ReadContent(entry *Entry) ([]byte, error) {
	return s.ReadContentAt(entry, 0)
}

func (s *Store) readVersionContent(id string, rec VersionRecord) ([]byte, error) {
	if rec.ChunkCount < 1 {
		return nil, ErrInvalidInput
	}
	var compressed bytes.Buffer
	for i := 0; i < rec.ChunkCount; i++ {
		var chunk []byte
		if err := s.kv.KVGet(blobKeyFor(rec, id, i), &chunk); err != nil {
			return nil, err
		}
		if _, err := compressed.Write(chunk); err != nil {
			return nil, err
		}
	}
	zr, err := gzip.NewReader(&compressed)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	body, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != rec.SHA256 {
		return nil, errors.New("channel artifact content hash mismatch")
	}
	return body, nil
}

func gzipChunks(content []byte) ([][]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(content); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	compressed := buf.Bytes()
	chunks := make([][]byte, 0, (len(compressed)/MaxChunkBytes)+1)
	for len(compressed) > 0 {
		n := min(MaxChunkBytes, len(compressed))
		chunk := make([]byte, n)
		copy(chunk, compressed[:n])
		chunks = append(chunks, chunk)
		compressed = compressed[n:]
	}
	return chunks, nil
}

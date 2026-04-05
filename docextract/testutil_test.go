// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package docextract

import (
	"archive/zip"
	"bytes"
)

// archiveZipWriter wraps archive/zip.Writer for test ZIP file creation
type archiveZipWriter struct {
	w *zip.Writer
}

func newArchiveZipWriter(buf *bytes.Buffer) *archiveZipWriter {
	return &archiveZipWriter{w: zip.NewWriter(buf)}
}

func (zw *archiveZipWriter) addFile(name, content string) error {
	f, err := zw.w.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write([]byte(content))
	return err
}

func (zw *archiveZipWriter) close() error {
	return zw.w.Close()
}

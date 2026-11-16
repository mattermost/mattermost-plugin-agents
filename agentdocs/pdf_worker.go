// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// pdfWorkerArg makes a binary that imports this package act as a PDF reader
// instead of its normal self: it reads one PDF from stdin and writes the
// result to stdout. The PDF library can loop or allocate without bound on a
// crafted file before any in-process check runs, so reading happens in a
// child process that can be killed and memory-limited.
const pdfWorkerArg = "--agentdocs-pdf-worker"

const (
	// maxPDFWorkerOutputBytes bounds the worker's result: at most
	// MaxTotalTextRunes characters of text plus JSON escaping.
	maxPDFWorkerOutputBytes = 4 * MaxTotalTextRunes * 6
	// pdfWorkerWaitDelay is how long to wait for the worker's pipes to close
	// after it has been killed.
	pdfWorkerWaitDelay = 2 * time.Second
)

func init() {
	if len(os.Args) == 3 && os.Args[1] == pdfWorkerArg {
		os.Exit(servePDFWorker(os.Args[2], os.Stdin, os.Stdout))
	}
}

type pdfWorkerResult struct {
	Text    string `json:"text"`
	Invalid string `json:"invalid"`
}

// servePDFWorker is the worker process: it returns the process exit code.
func servePDFWorker(name string, in io.Reader, out io.Writer) int {
	limitWorkerMemory()
	data, err := io.ReadAll(io.LimitReader(in, MaxDocumentBytes+1))
	if err != nil {
		return 1
	}
	var res pdfWorkerResult
	if text, err := readPDFText(name, data); err != nil {
		res.Invalid = err.Error()
	} else {
		res.Text = text
	}
	if json.NewEncoder(out).Encode(res) != nil {
		return 1
	}
	return 0
}

// runPDFWorker reads data in a worker process while holding a slot of
// pdfSlots. The worker is killed after pdfExtractTimeout or when ctx is done,
// so its slot is always released and stuck or runaway reads cannot pile up.
func runPDFWorker(ctx context.Context, name string, data []byte) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not find the PDF reader: %w", err)
	}

	slots, timeout := pdfSlots, pdfExtractTimeout
	wait := time.NewTimer(timeout)
	defer wait.Stop()
	select {
	case slots <- struct{}{}:
	case <-wait.C:
		return "", invalidf("the server is busy reading other PDF documents and could not read %q; try again in a moment", name)
	case <-ctx.Done():
		return "", fmt.Errorf("reading %q was canceled: %w", name, context.Cause(ctx))
	}
	defer func() { <-slots }()

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var stdout cappedBuffer
	cmd := exec.CommandContext(runCtx, exe, pdfWorkerArg, name)
	cmd.Env = []string{}
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = &stdout
	cmd.WaitDelay = pdfWorkerWaitDelay
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("could not start the PDF reader: %w", err)
	}
	waitErr := cmd.Wait()

	switch {
	case ctx.Err() != nil:
		return "", fmt.Errorf("reading %q was canceled: %w", name, context.Cause(ctx))
	case runCtx.Err() != nil:
		return "", invalidf("reading %q took too long; the PDF may be damaged or too complex", name)
	case waitErr != nil:
		return "", invalidf("could not read %q; the PDF may be damaged or too complex", name)
	}

	var res pdfWorkerResult
	if err := json.Unmarshal(stdout.buf.Bytes(), &res); err != nil {
		return "", invalidf("could not read %q; the PDF may be damaged or too complex", name)
	}
	if res.Invalid != "" {
		return "", invalidf("%s", res.Invalid)
	}
	return res.Text, nil
}

// cappedBuffer collects up to maxPDFWorkerOutputBytes and fails the write
// that goes over.
type cappedBuffer struct{ buf bytes.Buffer }

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > maxPDFWorkerOutputBytes {
		return 0, errors.New("PDF reader output is too large")
	}
	return c.buf.Write(p)
}

// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import "syscall"

// pdfWorkerMemoryBytes caps the address space of a PDF worker, so an
// allocation the PDF library makes from a crafted file fails inside the
// worker instead of exhausting the server's memory.
const pdfWorkerMemoryBytes = 2 << 30

func limitWorkerMemory() {
	limit := &syscall.Rlimit{Cur: pdfWorkerMemoryBytes, Max: pdfWorkerMemoryBytes}
	_ = syscall.Setrlimit(syscall.RLIMIT_AS, limit)
}

---
description: Call/recording transcription (ffmpeg + Whisper) and meeting summarization.
tags: [meetings, transcription, ffmpeg, summarization]
---

# meetings/AGENTS.md

Transcribes call recordings (ffmpeg → Whisper path) and summarizes them, integrating with `conversations`.

- **Missing ffmpeg disables transcriptions** (`service.go`) — guard for it; don't assume it's present.
- Recording/bot constants live in `service.go` (`CallsRecordingPostType`, `CallsBotUsername`, `ZoomBotUsername`). Transcript parsing (Zoom chat / WebVTT) is in `subtitles/`; summarization uses the legacy `chunking.SplitPlaintextOnSentences` helper, not the RAG chunker.
- `go test ./meetings/...`.

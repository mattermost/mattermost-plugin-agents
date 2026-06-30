# meetings/AGENTS.md

Scoped instructions for meeting transcription and summarization. Root rules in `/AGENTS.md` still apply.

## Dependencies

- Uses `bots` for LLM and transcription bot lookup.
- Uses `subtitles/` for VTT and Zoom chat parsing.
- Uses `chunking.SplitPlaintextOnSentences`, not embedding `ChunkText`.
- Uses `conversations` post-based orchestration, not `conversation.Service`.
- Requires `ffmpeg`; transcription is disabled when no usable binary is available.

## Flow

1. Calls or Zoom recording post is detected.
2. Audio is extracted or compressed for API limits.
3. Whisper transcription produces subtitles.
4. Transcript chunks are summarized.
5. Summary is streamed back through the conversation flow.

## Gotchas

- `WhisperAPILimit` drives re-encoding decisions for oversized recordings.
- `subtitles.FormatForLLM()` is the correct transcript formatter; do not use `/format/` for timed transcripts.
- Bot usernames and custom post types are part of integration behavior; keep tests/docs in sync if changed.
- Caption file IDs come from post props.

## Commands

- Meetings package: `go test -v ./meetings/...`
- Subtitle parser tests: `go test -v ./subtitles/...`

## Pointers

- Bot transcription config: `/bots/AGENTS.md`.
- Conversation orchestration: `/conversations/AGENTS.md`.

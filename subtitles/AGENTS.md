# subtitles/AGENTS.md

Scoped pointer for subtitle parsing and transcript formatting. Root rules in `/AGENTS.md` still apply.

- Full meeting transcription guidance lives in `/meetings/AGENTS.md`.
- Use `FormatForLLM` for prompt-ready timed transcripts.
- Do not route timed transcript formatting through `/format/`; that package is for Mattermost entities.
- Tests: `go test -v ./subtitles/...`

---
description: The LLM tool-call execution loop (call → execute → recall).
tags: [tools, llm, mcp, otel]
---

# toolrunner/AGENTS.md

Generic call → execute → re-complete loop for streaming completions. MCP-aware via `request.Context.Tools` (`ResolveTool` / `LookupTool`, `IsUnloadedMCPTool`).

- Capped at `MaxToolRounds = 10` (`limits/limits.go`).
- Emits a `"resolve tool"` OTel span (ToolName/ToolID/ToolStatus); `llm.ToolStore.ResolveTool` emits a nested span of the same name when called through here.
- Records MCP dynamic search/load telemetry. Higher-level tool *approval* and conversation orchestration live in `conversations/tool_approval.go`, not here.

Tests: `go test ./toolrunner/...`.

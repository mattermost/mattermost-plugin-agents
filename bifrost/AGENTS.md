# bifrost/AGENTS.md

Scoped instructions for building LLM requests in `bifrost/`. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

- Bifrost replays the primary's built request to each fallback, swapping only provider and model. Shaping that depends on the provider or model (reasoning, max tokens, image limits, message rules, prompt caching) is built per hop by the request builders and `hopRequestPlugin`; everything else (native tools, storage flags, API mode, structured output) must be valid for every service in the chain.
- Every hop's request satisfies that hop's provider message rules, for replayed history as well as the new turn: each tool call has a result paired by ID (not position), no text block is empty, and each declared image MIME type matches the bytes.
- Bifrost's `Error.Message` can carry raw upstream response bodies (especially for Azure); never pass raw Bifrost errors directly to HTTP responses, client stream events, audit records, or `span.RecordError` — log them server-side and return/record a sanitized error class or code.
- Bifrost's Azure client panics on a nil `AzureKeyConfig` when `APIURL` is empty; validate that an Azure service has a non-empty `APIURL` before constructing or calling a Bifrost client.

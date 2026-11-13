# Web search

Give agents web search through the provider's native tool (Professional) or Mattermost's built-in Brave, Google, or SearXNG search (Enterprise). If your provider has native web search, use that. Use built-in search for models that don't.

| | Native web search | Built-in web search |
|---|---|---|
| Who runs the search | The model provider | Mattermost, through Brave, Google, or SearXNG |
| Providers | Anthropic, OpenAI, Google Gemini, Google Vertex AI, and OpenAI Compatible or Azure with **Use Responses API** on | Any model that supports tool calling |
| Where to turn it on | Per agent, under **Advanced configuration** | **System Console > Plugins > Agents > Web Search**. Applies to agents that don't use native search. |
| License | Professional | Enterprise |
| Approval | No approval card | Asks for approval on every call |

When an agent has native web search enabled, the built-in tool isn't offered to it.

## Native web search

On the agent, open **Advanced configuration** and enable **Web Search** under **Native Claude Tools**, **Native Google Tools**, or **Native OpenAI Tools**. New agents have it on where the provider supports it and the license allows it. Providers that have no native tools ignore the setting.

Native tool activity, such as searches run and pages fetched, shows on the agent's reply as it streams and is kept with the conversation.

**In channels.** Native web search works in direct messages by default. To allow it in public and private channels too, turn on **Allow native web search in channels** under **AI Functions**. It needs a Professional license. It affects only native provider search, not MCP tools or other tools, and it works even when **Enable Channel Mention Tool Calling** is off.

### Anthropic web fetch and code execution

Anthropic agents have two more native tools:

- **Web Fetch** retrieves the full content of specific web pages and PDFs. See Anthropic's [web fetch tool](https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-fetch-tool) documentation for capabilities and pricing.
- **Code Execution** runs code in Anthropic's managed sandbox. Enabling it also lets web search and fetch filter results inside that sandbox. Without it, Mattermost restricts those tools so no sandbox is ever provisioned. Review Anthropic's [code execution tool](https://platform.claude.com/docs/en/agents-and-tools/tool-use/code-execution-tool) documentation for pricing and data retention before you turn it on.

OpenAI agents have **Code Interpreter**, which runs code in OpenAI's sandbox.

### Native tool limits

- Anthropic pauses very long provider-side tool loops. Mattermost doesn't resume them, so the response ends with what was produced.
- On Claude models older than 4.6, turning on web search or web fetch together with code execution leaves the sandbox unavailable.
- An Anthropic agent can share files it creates in the sandbox. It copies them to the sandbox's output directory, and they're attached to its reply. Files written elsewhere stay in the sandbox. The server's file attachment settings, size limits, the per-post cap, and the user's upload permission apply. OpenAI code interpreter files can't be retrieved yet.
- Later requests see native tool activity as a labeled summary, not the provider's original result blocks. Long commands and output are truncated, and the sandbox isn't reused.

## Built-in web search

Mattermost's own search tool lets an agent retrieve current information when the model has no native search.

1. Go to **System Console > Plugins > Agents > Web Search**.
2. Set **Enable Web Search** to **True**.
3. Choose a **Provider** and fill in its settings.

| Provider | Settings | Notes |
|---|---|---|
| **Brave Search** | **Brave API Key**, **Brave Result Limit** (1 to 10, default 5), **Brave API URL (optional)** | Returns summaries with each result, which reduces page fetches. You must have Brave's **Pro AI plan**. Using another Brave plan for AI use violates Brave's terms and can get the account suspended. |
| **Google Custom Search** | **Google API Key**, **Search Engine ID**, **Result Limit** (1 to 10, default 5), **API URL (optional)** | Uses Google's Custom Search API, which returns weaker results than google.com, and has strict daily quotas. Mattermost scrapes the pages it needs, and many sites block that. |
| **SearXNG (self-hosted)** | **SearXNG Base URL**, **SearXNG Result Limit** | Good for air-gapped or sovereign setups. The instance must allow the `json` format (`search.formats` in its settings). If it's on a private or reserved address, add it to the Mattermost Server's `AllowedUntrustedInternalConnections` setting. |

**Domain Denylist (optional)** takes a comma-separated list such as `example.com, spam-site.org`. It applies to every provider. Matching results are removed before the agent sees them, and the agent can't fetch pages from those domains.

Brave API keys come from [brave.com/search/api](https://brave.com/search/api/). For Google, create an API key in the Google Cloud Console with the Custom Search API enabled, and create a search engine at [Programmable Search Engine](https://programmablesearchengine.google.com). Its ID is the **Search Engine ID**.

### Limits

- An agent can run at most 3 searches per response. It's also told not to repeat a query.
- Queries shorter than 3 characters are rejected.
- Results carry citations that link to their sources.
- The agent can fetch the full page for a result, but only for a URL that came back in the search results. It's told to fetch at most 3 pages per request. Pages larger than 30 MB aren't fetched.
- Set **Enable Web Search** to **False** to remove the tool.

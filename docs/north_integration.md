# Cohere North Integration (POC)

This document describes how the Mattermost Agents plugin integrates with [Cohere North](https://cohere.com/north), Cohere's enterprise agents platform. North hosts models (e.g. North Large, Command A), agents with server-side tools (web search, data interpreter, connectors, MCP servers), and per-user permissions.

The plugin supports two integration modes that differ in **where the agent loop runs**:

| | Mode A: OpenAI-compatible | Mode B: Full delegation |
|---|---|---|
| Service type | **OpenAI Compatible** (existing) | **Cohere North** (new, experimental) |
| North endpoint used | `/api/v1/responses` (Open Responses spec) | `/api/v1/chat` (North native chat) |
| Agent loop runs in | **Mattermost** (the plugin's tool runner) | **Cohere North** (server-side) |
| Tools executed | Mattermost built-in + MCP tools, executed locally with the plugin's approval flow | The North agent's tools (web search, data interpreter, connectors, North MCP servers), executed inside North |
| North serves as | The model behind the plugin's own agent | The complete agent: model + tools + loop |
| Conversation state | Rebuilt by the plugin from the Mattermost thread each turn | Same (requests are stateless; the full thread is resent) |
| Reasoning/citations | Reasoning summaries via the Responses API | North thinking, tool plans, and tool activity surface in the Mattermost "thinking" UI; North citations render as citation annotations |
| Plugin code changes | None — configuration only | `north/` provider package |

Both modes can coexist: configure two services and give each Mattermost agent the one that fits.

## Mode A — North as an OpenAI-compatible model

North implements the [Open Responses specification](https://www.openresponses.org/) at `POST /v1/responses`, which is the same API the plugin already speaks for OpenAI-compatible services when **Use Responses API** is enabled. The plugin's agent loop works unchanged: the model requests function calls, the plugin executes Mattermost tools locally (with the usual approval flow), and results are sent back until the model produces a final answer.

```mermaid
sequenceDiagram
    participant U as User (Mattermost)
    participant P as Agents plugin (tool runner)
    participant N as North /v1/responses
    participant T as Mattermost tools (built-in + MCP)

    U->>P: message to agent
    P->>N: responses request (history + tool definitions)
    N-->>P: function_call items (streamed)
    P->>T: execute approved tool calls locally
    T-->>P: tool results
    P->>N: follow-up request with tool outputs
    N-->>P: final answer (streamed)
    P-->>U: streamed post
```

### Configuration

1. In the System Console Agents panel, add a service with type **OpenAI Compatible**.
2. **API URL**: your North instance's API base URL, e.g. `https://your-north-host/api`. (A trailing `/v1` is tolerated; the plugin issues requests to `<url>/v1/responses`.)
3. **API Key**: a North API token. For a quick start, use the token from your North instance's `/developer` page; for production, use a North service account or OAuth token — see the security notes below.
4. **Use Responses API**: **enabled** (required — North does not implement the legacy Chat Completions API).
5. **Default model**: a model name available on your North instance (e.g. `command-a-03-2025`). Note that North's `/v1/models` endpoint lists model *deployments*, not the underlying model names; ask your North operator which model names are routable.
6. Set token limits and a generous streaming timeout (e.g. 120 s), then create a Mattermost agent on this service with tools enabled.

## Mode B — Full delegation to a North agent

The **Cohere North** service type sends each turn to North's native chat API and lets North run the whole agent loop server-side. The plugin acts as a thin client:

- The Mattermost thread is mapped to North chat messages and sent as a **stateless** request, so the plugin's normal thread reconstruction, regeneration, and editing behavior keep working.
- The configured **North agent ID** (stored in the service's/agent's model field) selects which North agent handles the request; its preamble, model, temperature, and attached tools all live in North. Leave it blank to use the instance's default agent.
- North's server-side activity is surfaced in Mattermost: thinking content and tool plans stream into the "thinking" UI, tool invocations are narrated there, and North citations with source URLs render as citation annotations on the post.
- The provider never emits tool-call events, so the plugin's tool runner completes after a single call. Mattermost-side tools are intentionally out of the picture; create delegated agents with tools disabled.

```mermaid
sequenceDiagram
    participant U as User (Mattermost)
    participant P as Agents plugin (north provider)
    participant N as North /v1/chat (agent loop)
    participant T as North tools (web search, connectors, MCP…)

    U->>P: message to agent
    P->>N: stateless chat request (thread history + agent id)
    loop North-side agent loop
        N->>T: tool calls (server-side)
        T-->>N: tool results
    end
    N-->>P: SSE: thinking, tool plans, text, citations, usage
    P-->>U: streamed post with thinking + citations
```

### Configuration

1. Create (or pick) an agent on your North instance and note its agent ID (`GET /api/v1/agents`, or create one with `POST /api/v1/agents`).
2. In the System Console Agents panel, add a service with type **Cohere North (Experimental)**:
   - **API URL**: `https://your-north-host/api`
   - **API Key**: a North API token
   - **North agent ID**: the agent to delegate to (blank = instance default agent)
   - **Streaming Timeout Seconds**: idle timeout between stream events; North agent loops can run long, so 120 s is a reasonable floor.
3. Create a Mattermost agent on this service with **tools disabled**. A per-agent "model" override, when set, is interpreted as a different North agent ID.

## Choosing a mode

- Use **Mode A** when the agent should act on Mattermost data and tools (channel search, MCP servers registered in Mattermost) with Mattermost's approval flow, and North supplies the model.
- Use **Mode B** when the value lives in North — its enterprise connectors, permissions-aware search, data interpreter, and curated agents — and Mattermost is the conversational surface.

## Security and production notes

- **Credential scope**: in both modes every Mattermost user of the agent shares one North credential, so North sees a single identity. North applies that identity's permissions to tool and data access. Production deployments that need per-user data boundaries should exchange per-user tokens via North's OAuth/token-exchange flows instead of a shared token; that is future work for this POC.
- **Token lifetime**: tokens from North's `/developer` page are user JWTs with a limited lifetime and will need rotation. Prefer North service accounts / federated identities for anything long-lived.
- **Prompt privacy**: North's native stream can include `debug` events carrying the fully rendered prompt. The provider drops these events; they are never written to Mattermost posts.

## POC limitations (future work)

- Mode B forwards text only: file attachments and images are not mapped onto North's multimodal inputs.
- North tool approvals and MCP elicitations are not surfaced in Mattermost; tools that require interactive approval inside North will not complete.
- North conversation state is not reused across turns (the thread is resent statelessly). Mapping Mattermost threads to North `conversation` IDs would enable North-side history features.
- Structured-output requests (used by some internal plugin operations) rely on the plugin's prompt-based fallback rather than a native North JSON mode.
- A third integration shape — exposing a North agent as a *tool* (e.g. `ask_north`) that any Mattermost agent can call mid-loop — falls out naturally from Mode B's provider and may be worth prototyping.

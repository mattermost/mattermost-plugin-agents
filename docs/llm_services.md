# Configure LLM services

An LLM service connects Agents to a model provider: credentials, default model, token limits, fallback, and structured output. Several agents can share one service.

To add a service, go to **System Console > Plugins > Agents** and select **Add an AI Service**. Free and Professional licenses allow one service. Enterprise and above allow several. Provider-specific fields are in [Provider configuration](providers.md).

## Service settings

| Setting | Notes |
|---|---|
| **Service name** | A label that appears in the service list and in the agent editor's **AI Service** menu. |
| **Service type** | OpenAI, Anthropic, Google Gemini, Google Vertex AI, AWS Bedrock, OpenAI Compatible, Azure, Cohere, Cohere North, or Mistral. The list also shows Scale AI and asksage (Experimental), but this version of the plugin can't create a connection for either, so agents on those services don't work. Use one of the others. |
| Credentials and endpoint | Differ by type. See the [required settings](#required-settings-by-type). |
| **Default model** | The model agents on this service use unless the agent sets its own. For most types the field offers a live list from the provider, and you can type a custom name. |
| **Input token limit** | Maximum tokens sent to the model per request. |
| **Output token limit** | Maximum tokens the model may generate per response. When unset, Mattermost requests 8192. |
| **Streaming Timeout Seconds** | Timeout for streaming responses. Shown for OpenAI, OpenAI Compatible, Azure, Cohere, Cohere North, and Mistral. When unset, Mattermost uses 5 minutes. |
| **Use Responses API** | OpenAI Compatible and Azure only. See [Responses API](#responses-api). |
| **Fallback Service** | Another service to use when this one fails. Default is **No fallback**. Needs Enterprise Advanced. |
| **Structured output** | How JSON-schema requests are fulfilled. See [Structured output](#structured-output). |

A service is valid only when its required settings are filled in. An agent that uses an invalid or deleted service is marked **Inactive** on the Agents page.

### Required settings by type

| Type | Required |
|---|---|
| OpenAI | API key |
| OpenAI Compatible | API URL (API key is optional) |
| Anthropic | API key |
| Google Gemini | API key |
| Google Vertex AI | GCP Project ID, GCP Region (credentials optional) |
| AWS Bedrock | AWS Region (credentials optional) |
| Azure | API key, API URL |
| Cohere | API key |
| Cohere North | North instance URL, Service token |
| Mistral | API key |

## Token limits

**Input token limit** is a client-side cap. When it's greater than zero, Mattermost trims a request that would exceed it by dropping the oldest messages. It never drops system prompts or the newest message. At `0`, Mattermost does no client-side trimming and the provider rejects oversized requests.

For models whose limits the provider reports, Mattermost fills in the input and output limits, disables the fields, and shows **Auto-detected from provider**. The two limits are detected separately, so one can be automatic while the other stays editable. For an unknown or custom model, the fields are editable, and Mattermost uses the values you save. If you switch back from an auto-detected model to a custom one, Mattermost restores your earlier manual values.

## Responses API

OpenAI services always use OpenAI's Responses API. Cohere North does too. OpenAI Compatible and Azure services have a **Use Responses API** toggle, on by default. It enables provider-native tools, reasoning controls, and structured output on those endpoints.

Turn it off for an endpoint that only supports Chat Completions. To use OpenAI itself with Chat Completions, create an **OpenAI Compatible** service that points at the OpenAI endpoint and turn **Use Responses API** off, instead of using the **OpenAI** type.

## Fallback services

Set **Fallback Service** on a service to name a backup. A request that fails on the primary is retried on the fallback. The next request tries the primary first again.

- Chains work: A falls back to B, which falls back to C. Each service in the chain uses its own default model, endpoint, and settings.
- A chain with a cycle or a missing service fails visibly when the agent starts.
- A fallback changes only which service answers. It doesn't change user permissions, channel scope, or tool execution.
- Local or on-premises OpenAI Compatible services work as fallbacks, including chat-only endpoints when the primary uses the Responses API.
- Cohere North can be a fallback only for a primary that uses the Responses API and has no provider-native tools enabled. Otherwise the agent fails to start with a configuration error.
- Without an Enterprise Advanced license, a configured fallback is stored but ignored.

## Structured output

Some features ask the LLM for JSON that matches a schema. LLM Bridge callers can also send a `json_output_format` schema. The **Structured output** setting controls how that schema reaches the provider.

| Option | Stored value | Behavior |
|---|---|---|
| **Auto (recommended)** | `auto` or empty | Sends the schema natively only when the provider, model, and API path are known to support it. Everything else, including unrecognized models and custom endpoints, uses the prompt fallback. |
| **Native supported** | `native` | You assert the service accepts a native JSON schema. Use it for custom or OpenAI-compatible endpoints Mattermost can't detect. If you're wrong, the provider rejects those requests. |
| **Prompt fallback** | `prompt_fallback` | Never sends the schema natively. The schema becomes JSON instructions in the prompt. |

The key is `structuredOutputPolicy` on each service. An empty value means `auto`.

Mattermost decides once per request, across the whole fallback chain, because it converts the schema before it picks which service answers. A schema goes out natively only if every service in the chain is known to support it or is set to **Native supported**. If any one needs the prompt fallback, the whole request uses prompt instructions. Marking one service **Native supported** doesn't force native output for a chain that includes a service that can't do it.

This setting is per service and applies to agents and to LLM Bridge calls. The old per-agent structured output setting (`structuredOutputEnabled`) is deprecated and ignored.

# Multiple Model Instances with Custom Headers

This example demonstrates how to configure multiple instances of the same LLM provider (e.g., OpenAI) with different custom headers for each instance.

## Configuration Example

```json
{
  "services": [
    {
      "name": "openai-production",
      "type": "openai",
      "apiKey": "sk-prod-key-xxx",
      "defaultModel": "gpt-4",
      "customHeaders": {
        "X-Environment": "production",
        "X-Cost-Center": "marketing",
        "X-Request-Source": "mattermost-prod",
        "X-Priority": "high"
      }
    },
    {
      "name": "openai-development",
      "type": "openai", 
      "apiKey": "sk-dev-key-xxx",
      "defaultModel": "gpt-3.5-turbo",
      "customHeaders": {
        "X-Environment": "development",
        "X-Cost-Center": "engineering",
        "X-Request-Source": "mattermost-dev",
        "X-Debug": "true",
        "X-Priority": "low"
      }
    },
    {
      "name": "openai-proxy",
      "type": "openai_compatible",
      "apiKey": "sk-proxy-key-xxx",
      "apiURL": "https://proxy.company.com/v1",
      "defaultModel": "gpt-4",
      "customHeaders": {
        "X-Proxy-Auth": "Bearer company-proxy-token",
        "X-Department": "ai-ops",
        "X-Request-Source": "mattermost-proxy",
        "X-Billing-Code": "AIOPS-2024"
      }
    },
    {
      "name": "anthropic-production",
      "type": "anthropic",
      "apiKey": "sk-ant-api-xxx",
      "defaultModel": "claude-3-5-sonnet-20241022",
      "customHeaders": {
        "X-Environment": "production",
        "X-Provider": "anthropic",
        "X-Request-Source": "mattermost-claude"
      }
    }
  ],
  "bots": [
    {
      "id": "marketing-bot",
      "name": "marketing-assistant",
      "displayName": "Marketing Assistant",
      "service": {
        "name": "openai-production",
        "type": "openai",
        "apiKey": "sk-prod-key-xxx",
        "defaultModel": "gpt-4",
        "customHeaders": {
          "X-Environment": "production",
          "X-Cost-Center": "marketing", 
          "X-Request-Source": "mattermost-prod",
          "X-Priority": "high"
        }
      }
    },
    {
      "id": "dev-bot",
      "name": "development-assistant",
      "displayName": "Development Assistant", 
      "service": {
        "name": "openai-development",
        "type": "openai",
        "apiKey": "sk-dev-key-xxx",
        "defaultModel": "gpt-3.5-turbo",
        "customHeaders": {
          "X-Environment": "development",
          "X-Cost-Center": "engineering",
          "X-Request-Source": "mattermost-dev",
          "X-Debug": "true",
          "X-Priority": "low"
        }
      }
    },
    {
      "id": "secure-bot",
      "name": "secure-assistant",
      "displayName": "Secure Assistant",
      "service": {
        "name": "openai-proxy",
        "type": "openai_compatible",
        "apiKey": "sk-proxy-key-xxx",
        "apiURL": "https://proxy.company.com/v1",
        "defaultModel": "gpt-4",
        "customHeaders": {
          "X-Proxy-Auth": "Bearer company-proxy-token",
          "X-Department": "ai-ops",
          "X-Request-Source": "mattermost-proxy",
          "X-Billing-Code": "AIOPS-2024"
        }
      }
    },
    {
      "id": "claude-bot",
      "name": "claude-assistant",
      "displayName": "Claude Assistant",
      "service": {
        "name": "anthropic-production", 
        "type": "anthropic",
        "apiKey": "sk-ant-api-xxx",
        "defaultModel": "claude-3-5-sonnet-20241022",
        "customHeaders": {
          "X-Environment": "production",
          "X-Provider": "anthropic",
          "X-Request-Source": "mattermost-claude"
        }
      }
    }
  ]
}
```

## How It Works

1. **Per-Instance Configuration**: Each service configuration (`ServiceConfig`) includes its own `customHeaders` map
2. **Independent Headers**: Each model instance can have completely different custom headers
3. **Provider Agnostic**: Works with OpenAI, Anthropic, Azure, compatible APIs, etc.
4. **Bot-Level Inheritance**: Each bot uses the headers from its associated service

## Use Cases

### 1. Environment-Specific Headers
- Production bots use production headers with monitoring tags
- Development bots use debug headers and different routing

### 2. Cost Center Tracking
- Marketing bots include marketing cost center headers
- Engineering bots include engineering cost center headers

### 3. Proxy Routing
- Some instances route through corporate proxy with proxy auth
- Others connect directly with different authentication

### 4. Provider-Specific Requirements
- OpenAI instances might need organization headers
- Anthropic instances might need different tracking headers

## API Request Flow

When a bot makes an LLM request:

1. Bot configuration specifies which service to use
2. Service configuration includes custom headers
3. HTTP client wrapper automatically injects those headers
4. Each API request includes the service-specific headers

## Example: Different OpenAI Instances

```json
{
  "services": [
    {
      "name": "openai-team-alpha",
      "type": "openai",
      "apiKey": "sk-alpha-xxx", 
      "customHeaders": {
        "X-Team": "alpha",
        "X-Project": "chatbot-alpha"
      }
    },
    {
      "name": "openai-team-beta",
      "type": "openai", 
      "apiKey": "sk-beta-xxx",
      "customHeaders": {
        "X-Team": "beta",
        "X-Project": "chatbot-beta"
      }
    }
  ]
}
```

Both use OpenAI, but each has different headers for team tracking and billing.

## Important Notes

- Headers are applied **per service instance**, not per model type
- Each bot can use a different service instance with different headers
- Headers are static at configuration time
- All API requests for that service instance will include those headers
- This allows fine-grained control over request metadata per model instance

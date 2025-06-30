# Custom HTTP Headers Support

The Mattermost AI plugin now supports adding custom HTTP headers to all API requests made to LLM providers. This feature allows you to:

- Add authentication headers required by proxy services
- Include tracking or monitoring headers
- Add custom metadata headers for request identification
- Override default headers when needed

## Configuration

Custom headers are configured at the service level in the bot configuration. Add the `customHeaders` field to your service configuration:

```json
{
  "services": [
    {
      "name": "my-openai-service",
      "type": "openai",
      "apiKey": "sk-...",
      "defaultModel": "gpt-4",
      "customHeaders": {
        "X-Organization": "my-org",
        "X-Request-Source": "mattermost-ai",
        "X-Custom-Auth": "Bearer additional-token"
      }
    }
  ]
}
```

## Supported Providers

Custom headers are currently supported for:
- OpenAI (including Azure and compatible providers)
- Anthropic
- ASage

## Use Cases

### 1. Proxy Authentication
```json
"customHeaders": {
  "X-Proxy-Authorization": "Bearer proxy-token",
  "X-Forwarded-For": "mattermost-server"
}
```

### 2. Request Tracking
```json
"customHeaders": {
  "X-Request-ID": "mattermost-ai-{{timestamp}}",
  "X-Source-Application": "mattermost",
  "X-Environment": "production"
}
```

### 3. Custom Organization Headers
```json
"customHeaders": {
  "X-Organization-ID": "org-12345",
  "X-Department": "engineering",
  "X-Cost-Center": "ai-ops"
}
```

## Important Notes

- Custom headers are applied to **every** API request made to the LLM provider
- Headers will override any existing headers with the same name
- Header values are static and set at configuration time
- Headers are transmitted in plain text, so avoid including sensitive information
- The `Authorization` header can be overridden, but use caution as this may break authentication

## Security Considerations

- Custom headers are stored in the plugin configuration
- Ensure header values don't contain sensitive information that shouldn't be logged
- If using proxy authentication, consider using environment variables or secure configuration management
- Review logs to ensure custom headers don't leak sensitive data

## Example: Using with a Corporate Proxy

If your organization routes LLM requests through a corporate proxy that requires additional authentication:

```json
{
  "services": [
    {
      "name": "corporate-openai",
      "type": "openai", 
      "apiKey": "sk-...",
      "customHeaders": {
        "X-Proxy-User": "mattermost-service",
        "X-Proxy-Token": "corp-proxy-token-123",
        "X-Request-Origin": "mattermost.company.com"
      }
    }
  ]
}
```

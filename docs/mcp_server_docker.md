# Mattermost MCP Server Docker Image

> **WARNING: NOT FOR PRODUCTION USE**
>
> This Docker image is intended for **development, testing, and evaluation purposes only**. It provides a quick way to experiment with the Mattermost MCP Server without installing the full Agents plugin.
>
> **For production deployments**, use the embedded MCP Server that comes with the [Mattermost Agents plugin](admin_guide.md#mattermost-mcp-server). The embedded server provides:
> - Seamless integration with Mattermost authentication
> - Automatic configuration through the System Console
> - Enterprise features and support
> - Production-grade security and reliability

## Overview

The Mattermost MCP Server Docker image packages the standalone MCP (Model Context Protocol) server for easy deployment. This allows AI assistants like Claude Code, Claude Desktop, and other MCP-compatible clients to interact with your Mattermost instance.

For detailed information about the MCP Server's capabilities, available tools, and use cases, see the [Mattermost MCP Server documentation](admin_guide.md#mattermost-mcp-server).

## Quick Start

### Prerequisites

- Docker installed on your system
- A running Mattermost instance (v10.0+)
- A Personal Access Token (PAT) from your Mattermost account

### Creating a Personal Access Token

1. Log into your Mattermost instance
2. Go to **User Settings > Security > Personal Access Tokens**
3. Select **Create Token**
4. Give your token a descriptive name (e.g., "MCP Server")
5. Copy and save the token securely - you won't be able to see it again

### Running the Container

**STDIO Mode (Default)** - For piping to AI clients:

```bash
docker run -i --rm \
  -e MM_SERVER_URL=https://your-mattermost-server.com \
  -e MM_ACCESS_TOKEN=your-personal-access-token \
  mattermostdevelopment/mattermost-mcp-server:latest
```

**HTTP Mode** - For network-accessible server:

```bash
docker run -d --rm \
  -e MM_SERVER_URL=https://your-mattermost-server.com \
  -p 8064:8064 \
  mattermostdevelopment/mattermost-mcp-server:latest \
  --transport http --http-bind-addr 0.0.0.0 --http-port 8064
```

## Configuration

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `MM_SERVER_URL` | Yes | Your Mattermost server URL (e.g., `https://mattermost.example.com`) |
| `MM_ACCESS_TOKEN` | Yes (STDIO) | Personal Access Token for authentication |
| `MM_INTERNAL_SERVER_URL` | No | Internal URL for API communication (if different from public URL) |

### Command Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--transport` | `stdio` | Transport type: `stdio` or `http` |
| `--debug`, `-d` | false | Enable debug logging |
| `--logfile`, `-l` | - | Path to log file (logs to file in addition to stderr) |
| `--http-port` | 8080 | Port for HTTP server (HTTP mode only). Recommend using `8064` to avoid conflicts. |
| `--http-bind-addr` | 127.0.0.1 | Bind address for HTTP server (use `0.0.0.0` for external access) |
| `--site-url` | - | External URL for OAuth/CORS (HTTP mode with external access) |
| `--track-ai-generated` | false (stdio) / true (http) | Track AI-generated content in posts |
| `--dev` | false | Enable development mode with additional tools |

## Integration Examples

### Claude Code

Add the following to your Claude Code MCP configuration:

```json
{
  "mcpServers": {
    "mattermost": {
      "command": "docker",
      "args": [
        "run", "-i", "--rm",
        "-e", "MM_SERVER_URL=https://your-mattermost-server.com",
        "-e", "MM_ACCESS_TOKEN=your-personal-access-token",
        "mattermostdevelopment/mattermost-mcp-server:latest"
      ]
    }
  }
}
```

### Claude Desktop

Add to your Claude Desktop configuration file:

**macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
**Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "mattermost": {
      "command": "docker",
      "args": [
        "run", "-i", "--rm",
        "-e", "MM_SERVER_URL=https://your-mattermost-server.com",
        "-e", "MM_ACCESS_TOKEN=your-personal-access-token",
        "mattermostdevelopment/mattermost-mcp-server:latest"
      ]
    }
  }
}
```

### HTTP Mode for Web Clients

For MCP clients that connect over HTTP:

```bash
docker run -d \
  --name mattermost-mcp \
  -e MM_SERVER_URL=https://your-mattermost-server.com \
  -p 8064:8064 \
  mattermostdevelopment/mattermost-mcp-server:latest \
  --transport http --http-bind-addr 0.0.0.0 --http-port 8064
```

The MCP server will be available at `http://localhost:8064/mcp`.

## Available Tools

The MCP server provides the following tools to AI clients:

| Tool | Description |
|------|-------------|
| `read_post` | Read a specific post and its thread |
| `read_channel` | Retrieve recent posts from a channel |
| `search_posts` | Search across Mattermost content |
| `create_post` | Create new posts or replies |
| `create_channel` | Create new public or private channels |
| `get_channel_info` | Retrieve channel details |
| `get_team_info` | Retrieve team details |
| `search_users` | Find users by username, email, or name |
| `get_channel_members` | List all members of a channel |
| `get_team_members` | List all members of a team |

All operations respect Mattermost's permission system - users can only access content they're authorized to see.

## Building Locally

To build the Docker image locally:

```bash
git clone https://github.com/mattermost/mattermost-plugin-ai.git
cd mattermost-plugin-ai
make mcp-server-docker
```

This creates a local image tagged `mattermost-mcp-server:latest`.

## Troubleshooting

### Enable Debug Logging

```bash
docker run -i --rm \
  -e MM_SERVER_URL=https://your-mattermost-server.com \
  -e MM_ACCESS_TOKEN=your-token \
  mattermostdevelopment/mattermost-mcp-server:latest \
  --debug
```

### Connection Issues

- Verify your Mattermost server URL is accessible from the container
- Ensure your Personal Access Token is valid and has not expired
- Check that your Mattermost server allows API access

### Token Validation Errors

- Confirm the token was created correctly in Mattermost
- Verify the token has not been revoked
- Check that Personal Access Tokens are enabled on your Mattermost server (**System Console > Integrations > Integration Management**)

## Security Considerations

- **Token Security**: Your Personal Access Token grants access to your Mattermost account. Keep it secure and rotate it regularly.
- **Network Security**: When using HTTP mode with `--http-bind-addr 0.0.0.0`, the server is accessible from the network. Use appropriate firewall rules and consider TLS termination.
- **Not for Production**: This Docker image is not designed for production workloads. Use the embedded MCP Server in the Agents plugin for production deployments.

## Additional Resources

- [Mattermost MCP Server Documentation](admin_guide.md#mattermost-mcp-server) - Full documentation for the embedded MCP Server
- [Model Context Protocol](https://modelcontextprotocol.io/) - MCP specification and documentation
- [Mattermost Agents Plugin](https://github.com/mattermost/mattermost-plugin-ai) - Source repository

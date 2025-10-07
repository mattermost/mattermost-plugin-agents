# Mattermost MCP Server

A Model Context Protocol (MCP) server that provides AI agents and automation tools with secure access to Mattermost channels, users, and content.

## Features

- **MCP Protocol Support**: Implements the Model Context Protocol for standardized AI agent communication
- **Authentication**: Personal Access Token (PAT) authentication
- **Transport**: Configurable transport layer (stdio JSON-RPC for desktop clients, HTTP with SSE for web applications)
- **Comprehensive Mattermost Integration**: Read posts, channels, search, create content
- **Dual Mode Operation**: Runs standalone or embedded in the AI plugin

## Tools Available

The MCP server exposes the following tools to AI agents:

### `read_post`
Read a specific post and its thread from Mattermost.

**Parameters:**
- `post_id` (required): The ID of the post to read
- `include_thread` (optional): Whether to include the entire thread (default: true)

### `read_channel`
Read recent posts from a Mattermost channel.

**Parameters:**
- `channel_id` (required): The ID of the channel to read from
- `limit` (optional): Number of posts to retrieve (default: 20, max: 100)
- `since` (optional): Only get posts since this timestamp (ISO 8601 format)

### `search_posts`
Search for posts in Mattermost.

**Parameters:**
- `query` (required): The search query
- `team_id` (optional): Team ID to limit search scope
- `channel_id` (optional): Channel ID to limit search to a specific channel
- `limit` (optional): Number of results to return (default: 20, max: 100)

### `create_post`
Create a new post in Mattermost.

**Parameters:**
- `channel_id` (required): The ID of the channel to post in
- `message` (required): The message content
- `root_id` (optional): Root post ID for replies
- `props` (optional): Post properties
- `attachments` (optional): Array of file paths or URLs to attach to the post
  - **Note**: File paths only work with Claude Code; Claude Desktop cannot access local files

### `create_channel`
Create a new channel in Mattermost.

**Parameters:**
- `name` (required): The channel name (URL-friendly)
- `display_name` (required): The channel display name
- `type` (required): Channel type - 'O' for public, 'P' for private
- `team_id` (required): The team ID where the channel will be created
- `purpose` (optional): Channel purpose
- `header` (optional): Channel header

### `get_channel_info`
Get information about a channel. If you have a channel ID, use that for fastest lookup. If the user provides a human-readable name, try channel_display_name first (what users see in the UI), then channel_name (URL name) as fallback.

**Parameters:**
- `channel_id`: The exact channel ID (fastest, most reliable method)
- `channel_display_name` + `team_id`: The human-readable display name users see (e.g. 'General Discussion')  
- `channel_name` + `team_id`: The URL-friendly channel name (e.g. 'general-discussion')

### `get_team_info`
Get information about a team. If you have a team ID, use that for fastest lookup. If the user provides a human-readable name, try team_display_name first (what users see in the UI), then team_name (URL name) as fallback.

**Parameters:**
- `team_id`: The exact team ID (fastest, most reliable method)
- `team_display_name`: The human-readable display name users see (e.g. 'Engineering Team')
- `team_name`: The URL-friendly team name (e.g. 'engineering-team')

### `search_users`
Search for existing users by username, email, or name.

**Parameters:**
- `term` (required): Search term (username, email, first name, or last name)
- `limit` (optional): Maximum number of results to return (default: 20, max: 100)

### `get_channel_members`
Get all members of a specific channel with their details.

**Parameters:**
- `channel_id` (required): ID of the channel to get members for

### `get_team_members`
Get all members of a specific team with their details.

**Parameters:**
- `team_id` (required): ID of the team to get members for

### Development Tools (Dev Mode Only)

The following tools are only available when the `-dev` flag is enabled:

#### `create_user`
Create a new user account for testing scenarios.
- **Parameters:** `username`, `email`, `password`, `first_name` (optional), `last_name` (optional), `nickname` (optional), `profile_image` (optional): File path or URL to set as profile image (supports .jpeg, .jpg, .png, .gif)
  - **Note**: File paths only work with Claude Code; Claude Desktop cannot access local files

#### `create_team`
Create a new team.
- **Parameters:** `name`, `display_name`, `type` (O for open, I for invite only), `description` (optional), `team_icon` (optional): File path or URL to set as team icon (supports .jpeg, .jpg, .png, .gif)
  - **Note**: File paths only work with Claude Code; Claude Desktop cannot access local files

#### `add_user_to_team`
Add a user to a team.
- **Parameters:** `user_id`, `team_id`

#### `add_user_to_channel`
Add a user to a channel.
- **Parameters:** `user_id`, `channel_id`

#### `create_post_as_user`
Create a post as a specific user using username/password login. Simply provide the username and password of created users.

- **Parameters:** 
  - `username` (required), `password` (required)
  - `channel_id` (required), `message` (required)
  - `root_id` (optional), `props` (optional)
  - `attachments` (optional): Array of file paths or URLs to attach to the post
  - **Note**: File paths only work with Claude Code; Claude Desktop cannot access local files

## Installation and Usage

### Build

1. **Build the server:**
   ```bash
   # Using the Makefile (recommended)
   make mcp-server
   
   # Or manually from the project root
   go build -o bin/mattermost-mcp-server ./mcpserver/cmd/main.go
   ```

2. **Set up authentication:**
   - Create a Personal Access Token in Mattermost (User Settings > Security > Personal Access Tokens)
   - Note your Mattermost server URL

### Configuration Options

**Required:**
- `--server-url`: Mattermost server URL (or set `MM_SERVER_URL` env var)
- `--token`: Personal Access Token (or set `MM_ACCESS_TOKEN` env var for stdio transport)

**Optional:**
- `--internal-server-url`: Internal Mattermost server URL for API communication (or set `MM_INTERNAL_SERVER_URL` env var). Use this when the MCP server runs on the same machine as Mattermost to enable localhost communication while providing external URLs for OAuth clients.
- `--transport`: Transport type ('stdio' or 'http', default: 'stdio')
- `--logfile`: Path to log file (logs to file in addition to stderr, JSON format)
- `--debug`: Enable debug logging (recommended for troubleshooting)
- `--dev`: Enable development mode with additional tools for setting up test data
- `--version`: Show version information

**HTTP Transport Options (when --transport=http):**
- `--http-port`: Port for HTTP server (default: 8080)
- `--http-bind-addr`: Bind address for HTTP server (default: 127.0.0.1 for security, use specific IP for external access)
- `--site-url`: Public URL where clients will access the MCP server (used for OAuth metadata and origin validation)

**Notes**: 
- Token validation occurs at startup for fast failure detection
- Logging output goes to stderr to avoid interfering with JSON-RPC communication on stdout
- File logging (when `--logfile` is used) outputs structured JSON logs in addition to stderr
- Debug logging includes tool call tracing and detailed operation logs

### Integration with AI Clients

#### Claude Code Integration

```bash
claude mcp add mattermost -e MM_SERVER_URL=https://mattermost-url MM_ACCESS_TOKEN=<token> -- /path/to/mattermost-plugin-ai/bin/mattermost-mcp-server --dev --debug
```

#### Claude Desktop Integration

To use with Claude Desktop, add the server to your MCP configuration:

**macOS/Linux**: `~/.config/claude/claude_desktop_config.json`

**Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "mattermost": {
      "command": "/path/to/mattermost-plugin-ai/bin/mattermost-mcp-server",
      "args": ["--debug"],
      "env": {
        "MM_SERVER_URL": "https://your-mattermost.com",
        "MM_ACCESS_TOKEN": "your-pat-token"
      }
    }
  }
}
```

#### HTTP Transport Integration

For HTTP-based MCP clients and web applications, the server supports HTTP transport with OAuth authentication:

**Basic HTTP server setup:**
```bash
./bin/mattermost-mcp-server \
  --transport http \
  --server-url https://your-mattermost.com \
  --http-port 8080 \
  --debug
```

**External access via specific network interface:**
```bash
# Bind to specific network interface IP
./bin/mattermost-mcp-server \
  --transport http \
  --server-url https://your-mattermost.com \
  --http-bind-addr 192.168.1.100 \
  --http-port 3000 \
  --debug
```

**Environment variables:**
```bash
export MM_SERVER_URL=https://your-mattermost.com
export MM_INTERNAL_SERVER_URL=http://localhost:8065  # optional for localhost optimization
./bin/mattermost-mcp-server --transport http --http-port 8080 --site-url https://mcp.yourcompany.com
```

**Available endpoints:**
- `/mcp`: Main MCP communication endpoint (SSE and streamable HTTP)
- `/sse`: Server-Sent Events endpoint (backwards compatibility)
- `/message`: Message endpoint for SSE transport (backwards compatibility)
- `/.well-known/oauth-protected-resource`: OAuth metadata endpoint

### Development Mode

Development mode (`--dev` flag) enables additional tools for setting up realistic test data and user scenarios. This is particularly useful for Mattermost developers who need to bootstrap development environments or create sophisticated test scenarios.

**Enable development mode:**
```bash
./bin/mattermost-mcp-server --dev --server-url https://your-mattermost.com --token your-admin-pat-token
```

**Security Note:** Development mode should only be used in development environments with admin-level access tokens, never in production.

### Advanced Configuration: Internal Server URL

When the MCP server runs on the same machine as your Mattermost server, you can optimize performance and security by using the internal server URL option:

```bash
# MCP server and Mattermost on the same machine
./bin/mattermost-mcp-server \
  --server-url https://mattermost.company.com \
  --internal-server-url http://localhost:8065 \
  --token your-pat-token
```

**Environment variables:**
```bash
export MM_SERVER_URL=https://mattermost.company.com
export MM_INTERNAL_SERVER_URL=http://localhost:8065
export MM_ACCESS_TOKEN=your-pat-token
./bin/mattermost-mcp-server
```

**When to use:**
- MCP server deployed on the same server as Mattermost
- Docker containers on the same network
- Local development environments

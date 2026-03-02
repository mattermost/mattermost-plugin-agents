# Mattermost Embedded MCP Server Tool Classification

**Server:** Mattermost Embedded MCP Server  
**Endpoint:** `embedded://mattermost`

## Summary

- Total regular tools: 12
- READ-only tools: 8
- WRITE/action tools: 4
- Dev-mode-only tools: 4 (all WRITE)

## READ-Only Tools (Auto-Approve Eligible)

| Tool | Classification | Notes |
|------|----------------|-------|
| `read_post` | READ | Fetches a post/thread. |
| `read_channel` | READ | Fetches channel posts. |
| `get_channel_info` | READ | Fetches channel metadata. |
| `get_channel_members` | READ | Fetches channel member list. |
| `get_team_info` | READ | Fetches team metadata. |
| `get_team_members` | READ | Fetches team member list. |
| `search_posts` | READ | Searches posts. |
| `search_users` | READ | Searches users. |

## WRITE/Action Tools (Not Auto-Approved)

| Tool | Classification | Notes |
|------|----------------|-------|
| `create_post` | WRITE | Creates a channel post. |
| `dm_self` | WRITE | Creates a DM post. |
| `create_channel` | WRITE | Creates a channel. |
| `add_user_to_channel` | WRITE | Modifies channel membership. |

## Dev-Mode-Only Tools (Not Auto-Approved)

| Tool | Classification | Notes |
|------|----------------|-------|
| `create_user` | WRITE | Creates a user account. |
| `create_post_as_user` | WRITE | Creates a post as another user. |
| `create_team` | WRITE | Creates a team. |
| `add_user_to_team` | WRITE | Modifies team membership. |

## Vetted Auto-Approve List

The Mattermost built-in vetted server uses this permissive READ-only list:

- `read_post`
- `read_channel`
- `get_channel_info`
- `get_channel_members`
- `get_team_info`
- `get_team_members`
- `search_posts`
- `search_users`

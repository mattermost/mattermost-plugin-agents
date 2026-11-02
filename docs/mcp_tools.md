# Mattermost MCP tool reference

The built-in Mattermost MCP server provides 121 tools: 74 read-only and 47 that change state. This page lists every tool with its type, for admins deciding on approval policies and developers connecting external clients. The same tools serve agents inside Mattermost and, when an admin enables the HTTP endpoint, external MCP clients. See [Mattermost MCP server](mcp_server.md) for connecting a client.

## How the tools behave

- **Every call runs as the signed-in user.** A tool returns only what that user can see, and acts only where that user may. Channel membership, team membership, and roles apply.
- **Read-only tools work at every license level.** The 47 tools that change state need an Enterprise license. Below that, they're missing from the tool list, and a call returns a licensing message.
- **Approval.** For agents inside Mattermost, most read-only tools default to **Auto Run (DM)** and the rest ask. See [Connect agents to MCP servers](mcp_integrations.md#enable-and-approve-tools). External clients aren't covered by Mattermost approval policies. The client decides how to confirm calls.
- **Discovery.** With dynamic tool loading on, agents find tools with `search_tools` and load them with `load_tool`, instead of receiving every schema at once.
- **Posts the server creates are marked as AI-generated.**
- **Plugin tools aren't listed here.** Tools from plugin-registered MCP servers are configured separately. See [Connect agents to MCP servers](mcp_integrations.md#plugin-registered-mcp-servers).
- **Development tools.** Three more tools, `create_post_as_user`, `create_user`, and `create_team`, exist only on the standalone development server. They're not available in the embedded server or over HTTP.

## Posts and messages

| Tool | Type | What it does |
|---|---|---|
| `read_post` | Read-only | Read a specific post and its thread from Mattermost |
| `get_post_info` | Read-only | Get metadata and channel/team context for a single post (without the thread) |
| `list_pinned_posts` | Read-only | List the pinned posts in a channel |
| `list_saved_posts` | Read-only | List your saved (flagged) posts, optionally scoped to a channel or team |
| `create_post` | Changes state | Post a message in a channel or reply in a thread, optionally with inline text files. Needs the channel and team display names as a safety check |
| `dm` | Changes state | Send a direct message to a user, optionally with inline text files. Without a username, it messages you |
| `group_message` | Changes state | Send a message to a group conversation with at least two other users, optionally with inline text files |
| `update_post` | Changes state | Edit the message text of an existing post |
| `delete_post` | Changes state | Delete (soft-remove) a post by ID |
| `pin_post` | Changes state | Pin a post to its channel |
| `unpin_post` | Changes state | Unpin a post from its channel |
| `save_post` | Changes state | Save (flag) a post for yourself so you can find it later |
| `acknowledge_post` | Changes state | Add your acknowledgement to a post that requests one |

## Scheduled posts, reminders, and drafts

| Tool | Type | What it does |
|---|---|---|
| `list_scheduled_posts` | Read-only | List your pending scheduled posts for a team |
| `list_drafts` | Read-only | List your unsent message drafts for a team, including direct and group message drafts |
| `create_scheduled_post` | Changes state | Schedule a message to send to a channel (or thread) at a future time |
| `update_scheduled_post` | Changes state | Change a pending scheduled post's text or send time |
| `delete_scheduled_post` | Changes state | Cancel a pending scheduled post |
| `set_post_reminder` | Changes state | Set yourself a reminder about a post at a given time |
| `create_draft` | Changes state | Save a draft in a channel or thread for you to review and send yourself. Nothing is posted |
| `delete_draft` | Changes state | Delete your draft in a channel (or thread) |

## Reactions and emoji

| Tool | Type | What it does |
|---|---|---|
| `get_post_reactions` | Read-only | List the emoji reactions on a post and who reacted |
| `list_custom_emoji` | Read-only | List the server's custom emoji |
| `search_custom_emoji` | Read-only | Search custom emoji by name |
| `add_reaction` | Changes state | Add an emoji reaction to a post |
| `remove_reaction` | Changes state | Remove your emoji reaction from a post |

## Threads, mentions, and unread state

| Tool | Type | What it does |
|---|---|---|
| `get_threads` | Read-only | Get your threads inbox for a team: followed and unread threads |
| `get_mentions` | Read-only | Get recent posts containing unread at-mentions of you (Recent Mentions) |
| `get_unread_counts` | Read-only | Get your unread message, mention, and thread counts across teams, or for one team |
| `get_channel_unread` | Read-only | Get unread message and mention counts for one channel |
| `get_posts_around_unread` | Read-only | Get the posts surrounding your last-read line in a channel |
| `mark_channel_read` | Changes state | Mark a channel viewed/read to clear its unread badge |
| `mark_channels_viewed` | Changes state | Mark one or more channels viewed/read (bulk) |
| `mark_post_unread` | Changes state | Mark a channel unread starting at a specific post |
| `set_thread_follow` | Changes state | Follow or unfollow a thread |

## Channels

| Tool | Type | What it does |
|---|---|---|
| `read_channel` | Read-only | Read recent posts from a Mattermost channel, with history pagination |
| `get_channel_info` | Read-only | Get channel details by ID or name. A name that matches several teams returns every match |
| `get_channel_stats` | Read-only | Get a channel's statistics: member, guest, pinned-post, and file counts |
| `get_channel_member_counts` | Read-only | Get member counts for a batch of channels |
| `search_channels` | Read-only | Search channels by term |
| `list_team_channels` | Read-only | List a team's public and private channels |
| `list_archived_channels` | Read-only | List a team's archived (deleted) channels |
| `get_user_channels` | Read-only | Get the channels the signed-in user belongs to, including direct and group messages |
| `create_channel` | Changes state | Create a public or private channel in a team |
| `update_channel` | Changes state | Update a channel's display name, URL name, header, or purpose |
| `archive_channel` | Changes state | Archive (soft-delete) a channel |
| `restore_channel` | Changes state | Restore (unarchive) an archived channel |
| `convert_channel_privacy` | Changes state | Convert a channel between public and private |

## Channel members and settings

| Tool | Type | What it does |
|---|---|---|
| `get_channel_members` | Read-only | Get a channel's members, with paging |
| `get_channel_member` | Read-only | Get your (or a given user's) membership in a channel: roles, mute, last-viewed |
| `get_channel_members_by_ids` | Read-only | Get membership records for a specific set of users in a channel |
| `get_channel_members_by_status` | Read-only | Get a channel's members ordered by presence status (online first) |
| `get_user_channel_memberships` | Read-only | Get a user's channel membership records within a team (roles per channel) |
| `get_users_not_in_channel` | Read-only | Get team members who are not in a channel (candidates to add) |
| `search_users_in_channel` | Read-only | Search for users within (or excluding) a channel |
| `list_sidebar_categories` | Read-only | List your sidebar categories and their channels for a team |
| `add_channel_member` | Changes state | Add a user to a channel (channel membership) |
| `add_channel_members` | Changes state | Add several users to a channel (bulk; pairs with add_channel_member) |
| `remove_channel_member` | Changes state | Remove a user from a channel |
| `set_channel_mute` | Changes state | Mute or unmute a channel for yourself |
| `set_channel_favorite` | Changes state | Add or remove a channel from your favorites |
| `update_channel_notify_props` | Changes state | Set your per-channel notifications (desktop/push level, and unread/mute level) |

## Channel bookmarks

| Tool | Type | What it does |
|---|---|---|
| `list_channel_bookmarks` | Read-only | List a channel's bookmarks (pinned links and files) |
| `create_channel_bookmark` | Changes state | Add a link or file bookmark to a channel |
| `update_channel_bookmark` | Changes state | Edit a channel bookmark (name, link, or emoji) |
| `delete_channel_bookmark` | Changes state | Remove a channel bookmark |

## Users and profiles

| Tool | Type | What it does |
|---|---|---|
| `get_me` | Read-only | Get the profile of the signed-in user |
| `get_user` | Read-only | Get a single user's full profile by ID |
| `get_user_by_username` | Read-only | Get a user's profile by username |
| `get_user_by_email` | Read-only | Get a user's profile by email |
| `get_users_by_ids` | Read-only | Batch-fetch user profiles by a list of IDs |
| `get_users_by_usernames` | Read-only | Batch-fetch user profiles by a list of usernames |
| `get_user_stats` | Read-only | Get the server's total user count |
| `get_user_cpa_values` | Read-only | Get a user's Custom Profile Attribute (CPA) values |
| `list_cpa_fields` | Read-only | List the server's Custom Profile Attribute (CPA) field definitions |
| `search_users` | Read-only | Find users by username, email, or name |
| `update_user` | Changes state | Update profile fields (nickname, first name, last name, position) |

## Status and presence

| Tool | Type | What it does |
|---|---|---|
| `get_user_status` | Read-only | Get a user's presence: online, away, do not disturb (dnd), or offline |
| `get_users_statuses` | Read-only | Batch-fetch presence for a list of user IDs |
| `get_user_custom_status` | Read-only | Get a user's custom status (emoji, text, expiry) |
| `set_status` | Changes state | Set your own presence: online, away, offline, or do not disturb (dnd) |
| `set_dnd` | Changes state | Set your own Do Not Disturb (dnd) status, optionally until a given time |

## Teams and team members

| Tool | Type | What it does |
|---|---|---|
| `get_team_info` | Read-only | Get team details by ID or name |
| `get_team_members` | Read-only | Get a team's members, with paging |
| `get_team_member` | Read-only | Get a user's team membership record (roles) |
| `get_team_stats` | Read-only | Get a team's member counts (total and active) |
| `get_user_teams` | Read-only | Get the teams a user belongs to |
| `get_users_in_team` | Read-only | Get the member profiles of a team |
| `get_users_not_in_team` | Read-only | Get users who are not members of a team |
| `get_new_users_in_team` | Read-only | Get the most recently joined members of a team |
| `get_dm_common_teams` | Read-only | Get teams shared by all members of a direct message (DM) or group message (GM) channel |
| `search_teams` | Read-only | Search teams by name or display name |
| `search_users_in_team` | Read-only | Search for users within (or excluding) a team |
| `add_team_member` | Changes state | Add a user to a team (team membership) |
| `add_team_members` | Changes state | Add several users to a team (bulk) |
| `remove_team_member` | Changes state | Remove a user from a team |
| `update_team` | Changes state | Update team fields (display name, description, open-invite setting) |
| `invite_users_to_team` | Changes state | Email-invite users to a team |
| `invite_users_to_team_and_channels` | Changes state | Email-invite users to a team and specific channels |

## Files

| Tool | Type | What it does |
|---|---|---|
| `read_file` | Read-only | Read the text of a file attachment by file ID, with extracted text for PDF and Office documents and paged reads for large files |
| `get_file_info` | Read-only | Get metadata for a single file/attachment |
| `get_post_files` | Read-only | Get the file attachments on a post (metadata per file) |
| `get_file_link` | Read-only | Get a public link to a file. The link bypasses normal file access checks |
| `search_files` | Read-only | Search file attachments by filename/content within a team |
| `upload_file` | Changes state | Upload a file from a local path or URL. Available only on the local development server, so it can't upload files over HTTP or in the embedded server. Use `files` on `create_post` instead |

## Search

| Tool | Type | What it does |
|---|---|---|
| `search_posts` | Read-only | Search posts by keyword, with semantic results added when semantic search is on. Accepts team, channel, author, and date filters |

## Integrations

| Tool | Type | What it does |
|---|---|---|
| `get_bot` | Read-only | Get a single bot account's details |
| `list_bots` | Read-only | List bot accounts |
| `list_incoming_webhooks` | Read-only | List incoming webhooks |
| `list_outgoing_webhooks` | Read-only | List outgoing webhooks for a team or channel |

## Groups

| Tool | Type | What it does |
|---|---|---|
| `get_group_info` | Read-only | Get a group's metadata |
| `list_groups` | Read-only | List or search custom and LDAP-synced groups |
| `get_user_groups` | Read-only | Get the groups a user belongs to |
| `get_channel_groups` | Read-only | Get the groups synced to a channel |
| `get_team_groups` | Read-only | Get the groups synced to a team |
| `get_users_in_group_channels` | Read-only | Get the members of given group-message (GM) channels |

## Roles and permissions

| Tool | Type | What it does |
|---|---|---|
| `get_role` | Read-only | Get a role definition and its permission set |
| `get_channel_moderations` | Read-only | Get a channel's moderation settings (which actions members and guests may take) |
| `update_channel_member_roles` | Changes state | Promote or demote a channel member to or from channel admin |
| `update_team_member_roles` | Changes state | Promote or demote a team member to or from team admin |

## Agents

| Tool | Type | What it does |
|---|---|---|
| `list_agents` | Read-only | List the agents available to the user, with each agent's ID, display name, and username |

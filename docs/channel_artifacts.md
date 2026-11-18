# Save agent-built apps to a channel library

Channel members can save an interactive HTML app an agent built to the channel's Channel artifacts panel, reopen it later, and keep versions. This page is for System Admins.

The library is experimental. It stores references, not copies: each saved version points at the message and file the agent posted. The app stays available only while that message and file exist, and follows the message's retention.

## Turn it on

Turn on **Enable HTML artifacts** first. The library only holds HTML artifacts, and it is off until you also turn on the setting below.

| Setting | Config key | Default | What it does |
|---|---|---|---|
| Enable channel artifacts (experimental) | `enableChannelArtifacts` | Off | Shows the Channel artifacts panel and save controls, and lets agents list and read saved artifacts. Needs `enableHTMLArtifacts`. |
| Max artifacts per channel | `channelArtifactsMaxPerChannel` | 50 | Saving past the limit fails with "limit reached". |
| Versions to keep per artifact | `channelArtifactVersionsToKeep` | 20 | Saving a new version removes the oldest version records beyond this number. The current version is never removed. |

All three are under **System Console > Plugins > Agents > AI Functions**.

## Who can do what

Anyone who can read the channel can open its saved artifacts. Changing the library follows the channel bookmark permissions, so custom permission schemes apply:

| Action | Permission |
|---|---|
| Save an artifact, or an agent saves one | Add bookmarks (`add_bookmark_public_channel` or `add_bookmark_private_channel`) |
| Rename, add a version, restore a version | Edit bookmarks |
| Remove an artifact or a version | Delete bookmarks |

In direct and group messages, any member who isn't a guest can manage the library. Archived channels are read-only. System admins have these permissions in every channel.

## What can be saved

Only HTML files (`.html`, `.htm`) attached to a post written by one of Agents' own bots, in the same channel, and no larger than 5 MiB or the server's maximum file size if that's smaller. A person's attachment can't be saved. Saving the same content twice in a channel is refused and points at the existing artifact.

## Where the content lives

The library keeps a row per artifact and one per version, with the post ID, file ID, SHA-256, and size. It never stores the HTML.

- **Reads follow the source file.** To open, render, download, or let an agent read an artifact, Agents reads the file from the source post under the viewer's session. The file's download policy applies, including attribute-based access policies, and a denial fails closed: the artifact looks missing. The stored SHA-256 is checked on every read, and a mismatch makes the version unavailable.
- **Deleting or expiring the source makes the artifact unavailable.** If the post or file is deleted by a user, by message or file retention, or by a channel purge, the entry stays listed and is marked unavailable. Nothing is deleted automatically. Restoring an unavailable version fails.
- **Removing from the library only deletes rows.** Removing an artifact or a version, and pruning old versions, never deletes the post or file.
- **Channel deletion.** Rows for a permanently deleted channel are removed by the data retention job and when the plugin starts. Archived channels keep their library.

Back up the library with the plugin's database tables. See [Back up and automate](backup_and_automation.md).

## Agents and the library

Agents can read the library with the `list_channel_artifacts` and `get_channel_artifact` tools, which are read-only. See [Mattermost MCP tool reference](mcp_tools.md#channel-artifacts). The tools aren't offered while the library is off.

Agents can also manage the library directly with two built-in tools:

- `SaveChannelArtifact` saves HTML as a new artifact, or as a new version of an existing one. Given HTML content, it posts that content to the channel as a message from the agent, with the file attached, and records that message as the version. It can also record an HTML file already attached to one of the agent's posts in the channel.
- `RemoveChannelArtifact` removes a library entry.

Both tools ask for approval by default and work only in the channel the request came from. The requesting user must hold the matching bookmark permission, and the server checks it on every call. See [Approve tool calls](tool_approval.md).

Because each save through `SaveChannelArtifact` posts a message, the message is visible to everyone in the channel and follows the channel's retention.

## Audit records

Saves, renames, restores, and removals from the web app, and changes an agent makes, emit audit records with the channel, artifact, and version IDs. Records never contain titles or HTML.

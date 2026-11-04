# Create and manage agents

Agents are created and edited on the Agents page, not in the System Console. By default any user can create one, up to the license's agent cap.

Agents are stored in the Mattermost database and managed on the **Agents** page, not in the System Console. Services and the default agent are configured in the System Console. See [Configure LLM services](llm_services.md).

## Open the Agents page

- Select the product switcher (grid icon) and choose **Agents**, or open `/plug/mattermost-ai/agents`.
- From any agent picker, including the one in the composer's **AI Actions > Skills** menu, select **Manage**.

The page lists the agents you can use, on two tabs: **All agents** and **Your agents** (agents you created). Search filters by display name or username. Each row shows the avatar, display name, `@username`, and badges:

| Badge | Meaning |
|---|---|
| **All MCP tools** or **N tools** | The agent can use every MCP tool, or N selected tools. |
| **Inactive** | The agent is stored but not running. Hover for the reason. See [Inactive agents](#inactive-agents). |
| **Read only** | You can use the agent but can't edit it. |

**System Console > Plugins > Agents > AI Bots** shows a notice that points to this page. Only the **Limit agent version history** setting lives there.

## Who can create and edit agents

| Permission | What it allows |
|---|---|
| `manage_own_agent` | Create agents. Creators and agent admins can always edit and delete their agents, so removing the permission doesn't stop that. |
| `manage_others_agent` | Edit and delete any agent. |
| `manage_system` (System Admin) | Does everything above. Also manages agents with no creator, which are migrated v1 bots. |

By default the System User role includes `manage_own_agent`, so every user can create agents, up to the license's agent cap. Agents run on services the System Admin configured, so their usage is billed to the organization. To restrict creation, remove the permission from the System User role under **System Console > User Management > Permissions**, and grant it to specific roles. `manage_others_agent` is held only by System Admins by default.

A user can edit or delete an agent when they're its creator, listed in its **Agent admins**, hold `manage_others_agent`, or are a System Admin and the agent has no creator. The server applies the same rules to the API.

A user sees an agent only if they can use it. See [Visibility](access_control.md#visibility). Agent counts are capped by license. See [License requirements](admin_guide.md#license-requirements).

## Create an agent

1. On the **Agents** page, select **Create agent**.
2. Fill in the **Configuration**, **Access**, **MCPs**, and **Skills** tabs.
3. Select **Save**.

Saving creates the Mattermost bot account, stores the agent, and updates every node in a high availability cluster within seconds.

### Defaults for new agents

| Setting | Default |
|---|---|
| **AI Service** | The first service in the list |
| **Channel access**, **User access** | **Allow for all channels**, **Allow for all users** |
| **Automatically enable all MCP tools** | On |
| **Dynamic tool loading** | On |
| **Enable Vision**, **Enable Tools** | On, where the service type offers them |
| **Max tool turns** | 30 |
| Native web search | On where the provider supports it and the license allows it |
| Reasoning | On, medium effort (Anthropic uses the model's default) |

### Configuration tab

| Setting | Notes |
|---|---|
| **Display name** | Required. Shown in posts, pickers, and the agent list. Updating it updates the bot's display name. |
| **Agent username** | Required. The `@mention` handle. Starts with a letter, then lowercase letters, numbers, periods, hyphens, and underscores. The editor allows 22 characters. See [Renaming an agent](#rename-an-agent). |
| Avatar | Optional. Uploaded after the agent is saved. If the upload fails, the rest of the save still succeeds, so select the file and save again. Maximum 10 MB. |
| **AI Service** | Required. Shows the services from the System Console. A deleted service appears as "Unknown service (deleted)" until you pick another. Below Enterprise only the first service can be chosen. |
| **Model** | Optional. Overrides the service's default model. For OpenAI, Anthropic, Azure, OpenAI Compatible, Gemini, Vertex AI, and Cohere North the field offers a live list from the provider. |
| **Custom instructions** | Added to every request as part of the system prompt. Up to 100,000 characters. Use it for role, tone, vocabulary, and workflow. |

Switching services always clears **Model**. Switching to a service of a different type also resets native tools and reasoning to their defaults. A same-type switch keeps them.

Under **Advanced configuration**:

| Setting | Notes |
|---|---|
| **Dynamic tool loading** | The agent starts with search and load helpers and loads MCP tool schemas only when needed. Turn it off to expose the full tool list up front. See [Dynamic tool loading](mcp_integrations.md#dynamic-tool-loading). |
| **Max tool turns** | Maximum consecutive tool-call rounds per response, 1 to 250. After the last round, the agent answers without tools. Lower it for small models that loop on tools. Raise it for agents that chain many tools. A blank field saves the default. |
| **Enable Vision** | Lets the agent read attached images. Needs a vision-capable model. |
| **Enable Tools** | When off, the agent runs without tools and the **MCPs** and **Skills** tabs are disabled. Some features don't work without tools, such as free-form channel questions. Turn it off only for models that don't support tool calling. |
| **Native Claude Tools**, **Native Google Tools**, **Native OpenAI Tools** | Provider tools such as Web Search. See [Web search](web_search.md) and [Provider configuration](providers.md). |
| **Extended Thinking** or **Reasoning** | Reasoning controls. See [Provider configuration](providers.md). |

Which service types show these settings is in [Provider configuration](providers.md#capabilities-by-provider).

### Access tab

| Setting | Options |
|---|---|
| **Channel access** | **Allow for all channels**, **Allow for selected channels**, **Block selected channels**, **Block all channels** |
| **User access** | **Allow for all users**, **Allow for selected users**, **Block selected users**, **Attribute-based (access policy)** |
| **Agent admins** | Users who can edit and delete the agent, in addition to its creator. The creator is always an admin. |

Selected-user lists accept users and teams. Channel access applies to channels only, so a direct message with the agent is always allowed to users who pass the user rules. Details, including attribute-based access, are in [Control who can use agents](access_control.md).

### MCPs tab

Available only when **Enable Tools** is on.

- **Automatically enable all MCP tools.** The agent can use any MCP tool the requesting user can use, including tools added later. MCP server access policies, servers the user turned off, and license limits still apply.
- With it off, pick the specific tools. Tools that no longer exist are removed when you save.
- For an OAuth-backed server, select **Connect** here to sign in. Turning on a server that isn't connected yet grants the agent every tool the server exposes once someone connects.
- **Use service accounts for authentication.** Switches the agent's external MCP servers from per-user OAuth to shared admin-configured credentials. Only System Admins can turn it on. See [Service account authentication](mcp_integrations.md#service-account-authentication).

Granting a tool to an agent doesn't override approval policies. See [Tool approval and credentials](tool_approval.md).

### Skills tab

Available only when **Enable Tools** is on. It controls which [skills](skills.md) the agent can load on its own. The agent sees each available skill's name and description and loads the instructions when a request matches.

- **Enable skills** turns every skill on or off for this agent, including built-in skills.
- **Allow personal skills** lets each user's own skills be used by this agent in that user's direct messages with it. Personal skills are never used in channels.
- **Shared skills** lists skills shared with the workspace. Turn on each one for this agent. Skills shared later aren't added automatically. Enabling shared skills needs Enterprise. See [License requirements](admin_guide.md#license-requirements).
- **Description override** replaces a skill's description for this agent only, up to 1024 characters. Use it to say when this agent should use the skill.
- **Your personal skills** lists skills only you can use. Select **Share** to share one with the workspace and turn it on for this agent.

Built-in skills aren't listed. They're offered whenever the tools they need are available. A skill without a description isn't offered to the agent automatically.

## Edit an agent

Select a row, or open its menu and choose **Edit**. The editor saves every field on every tab each time. Changes apply from the next turn or mention. A reply that is already streaming finishes with the old settings.

If you leave the editor with unsaved changes, a **Discard changes?** prompt appears with **Discard** and **Keep editing**. A queued avatar change counts as a change.

### What's editable vs locked

While **Use service accounts for authentication** is on, only System Admins can edit channel access, user access, agent admins, MCP tool grants, **Automatically enable all MCP tools**, and the service account setting. Everything else stays editable by anyone who can manage the agent, who can also turn the setting off or delete the agent. While it's off, anyone who can manage the agent can edit everything.

### Rename an agent

Changing **Agent username** renames the existing bot. It keeps its user ID, direct messages, channel memberships, threads, and avatar. Saving shows a **Change agent username?** prompt listing the effects:

- Existing messages that mention the old `@username` keep that text and no longer link to the agent.
- People must mention the new username. Slash commands, scripts, and integrations that use the old name must be updated.
- The old username becomes available for someone else to claim.

The default agent setting selects an agent by bot user ID, so it keeps working after a rename. A rename fails with "This username is already taken" if any user or bot, including a deactivated one, holds the name. You can't rename agents defined only in a configuration file.

## Version history

Every save creates a numbered version. The agent always runs its latest version. There are no drafts. Creating, editing, and restoring each add a version. Some automatic changes, such as migrations, add a version labeled **System**. Agents from before version history get an **Initial version**.

Open the history from the version line under the agent's name, or the **Version history** button. Select a version to see who saved it, when, and its settings, with a **Changed** marker on settings that differ from the previous version. Only people who can manage the agent can see history, because versions include instructions.

To roll back, select a version and choose **Restore this version**. The restore is saved as a new version, so history is never rewritten. A restore goes through the same checks as a normal save:

- The version's AI service must still exist.
- License gates, access validation, and the service account rules apply.
- The username never changes.
- Versions don't store attribute-based access policies. Switching an agent away from attribute-based access deletes its policy, so restoring an earlier attribute-based version leaves the agent with no policy and nobody can use it. The editor opens the **Access** tab so you can add one.

### Delete versions

Versions keep everything an agent was saved with, including instructions you later removed. If a secret was saved, first edit the agent to remove it, then delete the versions that hold it. In the history window you can delete one version, select several, or choose **Delete all previous versions**. Deleted versions can't be recovered. The current version can't be deleted. Deleting an agent deletes all its versions.

### Keep a limited history

By default every version is kept. To keep only the most recent, set **Limit agent version history** to **True** in **System Console > Plugins > Agents** and set **Versions to keep per agent**. The count includes the current version, and turning it on sets 50. Saving the setting deletes older versions of every agent, and each later save prunes more. Turning it off doesn't restore deleted versions.

Restores and version deletions are recorded in the audit log. See [Monitor Agents](observability.md#audit-logging).

## Export and import an agent

Export copies an agent's purpose and tool setup to another server.

**Export.** In the editor header or the row menu, select **Export**. It downloads `<username>-v<version>.agent.json` for the current version. The file holds the suggested username and display name, custom instructions, **Enable Tools**, **Max tool turns**, dynamic tool loading, **Automatically enable all MCP tools**, and the granted MCP tools, each identified by its server URL. It doesn't hold the service, model, reasoning, native tools, vision, access rules, admins, the service account setting, the avatar, any IDs, or credentials. Treat the file like the instructions, since they're included.

**Import.** Select **Import** on the **Agents** page to start a new agent from a file, or in the editor to load a file into an existing agent. Nothing is saved until you select **Save**, and the save goes through the usual checks. Review the imported instructions first, because they become the agent's instructions. Importing into an existing agent replaces only the settings the file carries.

MCP tools in the file are matched to your servers by URL, ignoring surrounding whitespace and a trailing `/`. Tools of servers you haven't configured are left out, and the editor names those servers. Files over 2 MiB, files that aren't agent exports, and unsupported versions are rejected.

## Delete an agent

Open the row menu, select **Delete**, and confirm. This deactivates the bot account, so mentions no longer reach it, and removes the agent from pickers on every node. It can't be undone. Existing posts remain.

## Inactive agents

An **Inactive** agent is stored but not running. The badge tooltip gives one of these reasons:

| Reason | Fix |
|---|---|
| The service was deleted or is missing required settings | Edit the agent and choose a current service, or finish the service configuration. |
| The service isn't active on the license | Free and Professional use only the first service. Choose that service, or upgrade. |
| The license's agent cap is full | Agents created earlier take the slots. Delete one or upgrade. |
| The agent's configuration is incomplete | Edit the agent to fix it. |

Attribute-based access denial doesn't show as **Inactive**. People who are denied don't see the agent.

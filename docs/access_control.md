# Control who can use agents

You can restrict who can use an agent, where it can be used, and which users can reach an LLM service or an MCP server. This page covers allow and block lists on each agent, and attribute-based access policies for agents, services, and MCP servers. Access rules decide who can use a resource. Which tool calls need approval is a separate control. See [Tool approval and credentials](tool_approval.md).

## Rules at a glance

| What | Where you set it | Restricts | License |
|---|---|---|---|
| Who can use an agent | Agent editor, **Access** tab, **User access** | Users and teams | Professional for allow and block lists, Enterprise Advanced for attribute-based |
| Where an agent works | Agent editor, **Access** tab, **Channel access** | Channels | Professional |
| Who can edit an agent | **Agent admins**, `manage_own_agent`, `manage_others_agent` | Managers | None. See [Create and manage agents](managing_agents.md#who-can-create-and-edit-agents). |
| Who can use an LLM service | Service panel, **Access policy** | Users | Enterprise Advanced |
| Who can use an MCP server | MCP **Configuration** tab, **Access policy** | Users | Enterprise Advanced |
| Where an agent auto-replies | Channel settings, **Agents** tab | Channel | Enterprise Advanced. See [Use agents in channels](agents_in_channels.md#auto-reply). |

## Allow and block lists

On an agent's **Access** tab:

- **User access:** **Allow for all users** (default), **Allow for selected users**, **Block selected users**, or **Attribute-based (access policy)**. Selected-user lists take individual users and whole teams. Team membership is checked when the person uses the agent.
- **Channel access:** **Allow for all channels** (default), **Allow for selected channels**, **Block selected channels**, or **Block all channels**. **Block all channels** makes the agent direct-message only.

The server enforces these rules for @mentions, direct messages, slash commands, and tool-driven access. Channel rules don't apply to direct messages. Creating an agent with restrictions needs a Professional license. Opening an agent back up to everyone, and editing an existing list, work at any license level.

## Attribute-based access control

Attribute-based access control (ABAC) restricts access with policies written against user attributes, for example `user.attributes.department == "engineering"`. You don't maintain user or team lists. A policy always grants or denies the `use` action on one resource.

Requirements:

- An Enterprise Advanced license.
- Attribute-based access control enabled on the Mattermost Server. The plugin checks, and hides all ABAC settings when the server doesn't offer it.
- User attributes (custom profile attributes) configured, because policies refer to them.

### Policies for each resource

| Resource | Where you author it | Behavior |
|---|---|---|
| Agent | Agent editor, **Access** tab. Set **User access** to **Attribute-based (access policy)**, save, then define the policy. | The policy is the only user gate. The agent's allow and block lists are ignored. |
| LLM service | **System Console > Plugins > Agents**, on the service panel | Restricts every agent that uses the service, on top of each agent's own rules. Also affects who sees the service and its agents. |
| MCP server | **System Console > Plugins > Agents > Model Context Protocol (MCP)**, **Configuration** tab. Built-in and plugin servers are under **Built-in & plugin servers**. | Applies to remote servers, the built-in Mattermost Server, and plugin-registered servers. Users who are denied lose that server's tools without notice. |

Denying the built-in Mattermost Server removes nearly all Mattermost tools for the users it matches.

Each editor has **Simple** and **Advanced** modes. Advanced takes a CEL expression, and is for System Admins. If a policy uses an expression the simple editor can't show, only a System Admin can edit it, and anyone who can manage the agent can remove it. The editor warns you if the policy would exclude you.

Who can author:

| Policy | Who |
|---|---|
| Agent policy | The agent's creator, its agent admins, and users with `manage_others_agent`. While **Use service accounts for authentication** is on, only System Admins. |
| Service policy | System Admins |
| MCP server policy | System Admins |

### How a request is decided

- **No policy on a service or MCP server:** unrestricted. Installing or upgrading the plugin changes nothing until you write a policy.
- **No policy on an agent in attribute-based mode:** everyone is denied. Define the policy right after you switch the mode. This also applies after a version restore. See [Create and manage agents](managing_agents.md#version-history).
- **A policy exists:** it decides. Matching users are allowed, others are denied.
- **The access engine is unavailable** (for example the license lapsed, or an evaluation fails): the request is denied. Agents in a legacy mode with no policy keep using their allow and block lists.
- **Server without ABAC:** legacy agents keep their lists, and services and MCP servers are unrestricted. Agents in attribute-based mode are unusable for everyone until you upgrade or change the mode.

The access tab's hint says "Until a policy is defined, all users can use this agent." That doesn't match the server, which denies. Define a policy first.

### Visibility

Lists and pickers show only what a user can use, with one exception. System Admins can see services and the agents that use them even when a service policy denies them, so they can audit and repair bindings. Everyone else, including an agent's creator and agent admins, doesn't see agents or services they can't use. Agents denied by a policy don't show as **Inactive**. They're absent. If an agent admin loses service access, they can't see or fix the agent. A System Admin must restore access or change the service.

### Policy lifecycle

- Deleting an agent removes its policy.
- Deleting a service or an MCP server from the configuration doesn't delete its policy. If you recreate a resource with the same ID, the old policy applies again. Remove the policy first if you don't want that.
- A plugin MCP server's policy is tied to the plugin ID. It survives unregistering, and reattaches if the same plugin registers again.
- Policy changes are recorded in the audit log. See [Monitor Agents](observability.md#audit-logging).

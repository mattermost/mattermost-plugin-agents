# Run a project in a channel with its own agent

A project channel is a channel led by its own hidden agent, which keeps a board, a checklist, a status note and artifacts for the project and works only inside it. This page is for System Admins.

Project channels are experimental and off by default.

## Turn it on

| Setting | Config key | Default | What it does |
|---|---|---|---|
| Enable project channels | `enableProjectChannels` | Off | Adds a **Project** option to the **Create a new channel** dialog and lets agents offer to start a project from a direct message. |
| Project agent service | `projectDefaultServiceID` | Empty | The LLM service every new project agent uses. Project channels stay off until it is set. |
| Project agent model | `projectDefaultModel` | Empty | The model new project agents use. Empty uses the service's default model. |

Project agents run with service account authentication in scoped channel mode, so the server must support scoped sessions. They also rely on ambient auto-replies, channel mention tool calling, HTML artifacts and channel artifacts. The board needs the Boards plugin with MCP tools, and the checklist needs the Playbooks plugin. Cursor runs need the Cursor plugin.

## What a project agent can do

When someone creates a project, Agents creates the channel (or uses the channel they just created), creates a hidden agent for it, adds the agent to the channel as a channel admin and turns on ambient replies there. The agent then opens a setup thread to write a charter, create the board and checklist once, and set the status note.

The agent is limited to its project:

- It reads and writes only the project channel and its board channel. If its bot account is found in any other channel, every turn is refused until it is removed.
- Board tools are bound to the project's board, and checklist tools to the project's run. It can create one board and one checklist, and can't delete cards, list other boards, or change views and fields.
- Starting a Cursor run or adding a follow-up always asks the person who requested it, and is never offered when nobody is present to approve it.
- Project agents don't appear in agent lists, pickers, the LLM Bridge or the Mattermost MCP server's agent list.

## Check-ins

The project lead can set a check-in schedule as a five-field cron expression (minute, hour, day of month, month, weekday) in a time zone. At each check-in the agent posts a short update in a new thread, acting on behalf of the lead. Check-ins pause when the lead leaves the channel or becomes a guest; transfer the lead to resume them.

## Ending a project

The lead, or anyone who can manage the channel's roles, can end the project. The agent posts a wrap-up and saves it to the channel's artifacts, unless the person ending it skips the wrap-up. Agents then finishes the checklist run, archives the board channel and the project channel, and removes the project agent. Archiving a project channel directly ends its project without a wrap-up.

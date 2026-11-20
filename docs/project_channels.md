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

When someone creates a project, Agents creates the channel (or uses the channel they just created), creates a hidden agent for it, adds the agent to the channel as a channel admin and turns on ambient replies there. The agent answers unmentioned posts that report progress, a blocker, a decision or a date change, or that should update the board, checklist or status note, and stays out of casual conversation. Channel auto-reply instructions, when set, replace this rule. The agent then opens a setup thread to write a charter, create the board and checklist once, and set the status note. In that thread the lead, anyone who can manage the channel's roles, and System Admins can also have the agent update its name and instructions; nobody else can change them, and the agent never changes them on a turn nobody is present for.

The agent is limited to its project:

- It reads and writes only the project channel and its board channel. If its bot account is found in any other channel, every turn is refused until it is removed. The exception is the team's default channel (usually Town Square), which Mattermost keeps every team member in; the agent still can't read it.
- When someone in the project channel is talking to it, it saves and removes artifacts there without asking for approval. On check-ins and other turns with nobody present, it can't save or remove artifacts.
- Its Mattermost tools are reading the project's channels, posts, users, bookmarks and artifacts, adding reactions, and creating and updating bookmarks in the project channel. New project agents get this list when they're created, and existing ones get it when the plugin starts.
- Board tools are bound to the project's board, and checklist tools to the project's run. It can create one board and one checklist, and can't delete cards, list other boards, or change views and fields.
- Members of the project channel are added to the board channel when the board is created, and as they join, so they can open the board. People who leave the project channel are removed from the board channel.
- After saving the charter to the channel's artifacts, it links the charter to the project so the project panel shows it.
- Opening a Cursor run thread, starting a Cursor run and adding a follow-up always ask the person who requested them. Cursor tools run as that person once approved, and are never offered when nobody is present to approve them. Until someone replies in a new run thread, the agent can only read there.
- Each Cursor run gets one Cursor agent, started from inside its run thread, which records the agent. Reading or following up on a Cursor agent works only for agents the project's run threads started.
- In a project channel, changing the agent's own instructions, name or tools, or completing setup, always asks the person who requested it.
- It treats the status note as information from the channel, not as instructions, and doesn't see the note in the setup thread, where it can change its own instructions.
- Project agents don't appear in agent lists, pickers, the LLM Bridge or the Mattermost MCP server's agent list. Their bot accounts are regular Mattermost bot accounts, so the Mattermost MCP server's `list_bots` and `get_bot` tools still return them. They can't be changed or deleted from the agent settings while the project is running; end the project instead. If a project agent remains after its project ended, it can be deleted from the agent settings.

## Check-ins

The project lead can set a check-in schedule from the project panel as a five-field cron expression (minute, hour, day of month, month, weekday) in a time zone. The agent can't change the schedule. At each check-in the agent posts a short update in a new thread, acting on behalf of the lead. Check-ins pause when the lead leaves the channel or becomes a guest; transfer the lead to resume them.

## Ending a project

The lead, or anyone who can manage the channel's roles, can end the project. The agent posts a wrap-up and saves it to the channel's artifacts, unless the person ending it skips the wrap-up. Agents then finishes the checklist run, archives the board channel and the project channel, and removes the project agent. Archiving a project channel directly ends its project without a wrap-up. If ending a project stops partway, ending it again after 10 minutes finishes the cleanup without writing another wrap-up.

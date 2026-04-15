<!--
Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
See LICENSE.txt for license information.
-->

# Channel Summaries

Channel Summaries help you catch up on activity in the current channel without manually reading every post. Use the **Ask Agents about this channel** button in the channel header to summarize recent activity, focus on a time range, or ask a custom question about the conversation.

Channel Summaries require a license. See the [license requirements](../admin_guide.md#license-requirements) for details.

## Use Channel Summaries

To open Channel Summaries:

1. Open the channel you want to analyze.
2. Select **Ask Agents about this channel** in the channel header.
3. Choose the agent you want to use in **GENERATE WITH:** if multiple agents are available.
4. Select one of the available actions, or enter a custom prompt.

The response is generated in the **Agents** pane, and only you can view it.

## Available actions

Channel Summaries provide several ways to analyze the current channel:

- **Summarize unreads**: Summarize messages posted since you last viewed the channel.
- **Summarize last 7 days**: Summarize recent discussion from the last week.
- **Summarize last 14 days**: Summarize discussion from the last two weeks.
- **Select date range to summarize**: Summarize activity between specific start and end dates.
- **Custom prompt**: Enter a question such as "What decisions were made in this channel?" or "What action items are still open?"

## Ask custom questions

Use the text box in the popover to ask a targeted question about the current channel. For example, you can ask an agent to:

- Identify decisions, risks, or blockers.
- Extract action items and owners.
- Explain the status of a project or incident.
- Summarize a discussion for someone joining late.

This helps you move beyond a general summary when you need a specific answer based on channel context.

## Choose the right entry point

Mattermost Agents offers more than one way to summarize channel activity:

- Use **Ask Agents about this channel** when you want to analyze the current channel with flexible options such as custom prompts, date ranges, or recent activity summaries.
- Use **Ask AI** at the **New Messages** line when you want to quickly summarize unread messages in a channel. See [Summarize unread channels](../user_guide.md#summarize-unread-channels).

## Tips

- Choose the agent that best matches your task, such as a general assistant or a more specialized team agent.
- Ask narrow questions when you want a focused answer, such as decisions, risks, next steps, or customer feedback.
- Use date ranges to reduce noise in busy channels and focus on a specific period.

Contact your system admin if **Ask Agents about this channel** is not available in your Mattermost workspace.

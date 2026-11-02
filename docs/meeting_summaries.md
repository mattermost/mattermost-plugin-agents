# Meeting summaries

Agents can turn a meeting transcript from Mattermost Calls or Zoom into a structured summary with key discussion points and action items. This page covers how to create a summary, what it needs, and what to check if the button is missing. Meeting summaries need an Enterprise license or higher.

## Create a summary

1. Record the meeting. For Calls, [start a call and record it](https://docs.mattermost.com/collaborate/make-calls.html) with transcription turned on. For Zoom, see [Zoom prerequisites](#zoom-prerequisites).
2. When the transcript is posted, select **Create meeting summary** on the post.
3. The agent replies in a direct message thread with you: it posts a link to the transcript, then streams the summary. Open the thread in the Agents pane.

![The Create meeting summary button on a Zoom transcript post.](img/create-meeting-summary-zoom.png)

The summary has a summary section, key discussion points, and action items if there are any. It doesn't list participants or the date.

![A meeting summary in a direct message thread with the agent.](img/meeting-summary-thread.png)

While it generates you can select **Stop Generating**, and afterward **Regenerate**. Reply in the thread to ask the agent for changes. To share the summary, select **Post summary**. That posts it to the original call thread, so you need permission to post in that channel. The posted message says the agent created it and you posted it.

## Zoom prerequisites

A system admin must [enable and configure the Zoom plugin](https://docs.mattermost.com/integrate/zoom.html), and Zoom cloud recordings and transcripts must be turned on in Zoom. Then:

1. Subscribe a channel to a recurring Zoom meeting with `/zoom subscription add [meeting ID]`, or start a meeting from the Zoom button in the right-hand sidebar.
2. Record the meeting.
3. When the transcript file is posted to the channel, select **Create meeting summary** on that post.

## How it works

- **Which agent:** the workspace's default agent writes the summary, not the agent you selected in a picker. Set the default in **System Console > Plugins > Agents** under **Default bot**.
- **Which transcripts:** only posts from the `calls`, `zoom`, and `google-meet` bot accounts can be summarized. Zoom chat exports and WebVTT transcripts are supported.
- **No tools:** the agent summarizes the transcript text and doesn't call tools.
- **Privacy:** the transcript text is sent to the LLM service the default agent uses. With a self-hosted model, it stays in your environment. See [Data handling](data_handling.md).
- **Who sees it:** the summary stays private to you until you select **Post summary**.

## If the button is missing or the summary fails

| Symptom | Likely cause |
|---|---|
| Selecting **Create meeting summary** does nothing | The server doesn't have an Enterprise license or higher. |
| No **Create meeting summary** button | The Calls or Zoom plugin adds the button, so check that the plugin is enabled, that the transcript was posted, and that the post comes from the `calls`, `zoom`, or `google-meet` bot. |
| "Sorry! Something went wrong" reply | Check the Mattermost Server log for the error. The default agent might be inactive or its LLM service unreachable. |
| The summary comes from a different agent than you expected | Meeting summaries always use the default agent. Change it in the System Console. |

For Calls recording and transcription, see the Calls documentation.

# Set up dictation

Let users dictate messages with a microphone button in every Mattermost message box. Browsers stream audio directly to OpenAI, which transcribes it while the user speaks.

Dictation needs an Enterprise license or above. It uses its own transcription service, chosen separately from the services your agents use, so a server can run agents on Anthropic and dictation on OpenAI. Users don't need an agent to dictate.

## How it works

1. A user selects the microphone button and allows microphone access.
2. The browser sends a WebRTC connection offer to the plugin. The plugin checks the license, the settings, the user's access to the service and the limits below, then opens the session with OpenAI using the service's API key. It returns OpenAI's answer to the browser and keeps a control connection to the session. The browser never receives an OpenAI credential.
3. The browser streams the microphone audio to OpenAI over WebRTC.
4. OpenAI sends the transcript back to the browser, which types it into the message box at the cursor.

The plugin ends a session after 10 minutes, and also ends a session whose settings a client changed (model, language, turn detection or session type). The user sees why dictation stopped.

## Set it up

1. Open **Agents** from the product switcher and select **Settings**. On **AI services**, add an OpenAI service with an API key and save, if you don't have one. The service doesn't need an agent. See [Configure LLM services](llm_services.md).
2. On **Dictation**, set **Enable dictation** to **True**.
3. Select the **Transcription service**. Only saved OpenAI services are listed.
4. Check the **Transcription model** and optionally set a **Language**, then save.

Guest accounts and bot accounts can't dictate. An access policy on the transcription service applies to dictation too: users it denies don't see the button. Service access policies need Enterprise Advanced; without one, every other signed-in user can dictate. See [Policies for each resource](access_control.md#policies-for-each-resource).

| Setting | Config key | Default | Details |
|---|---|---|---|
| Enable dictation | `dictation.enabled` | Off | Shows the microphone button to users who can use the transcription service. Can't be turned on below Enterprise. |
| Transcription service | `dictation.serviceID` | None | The ID of an OpenAI service. Saving with dictation on fails when the service is missing, isn't an OpenAI service, has no API key, or has an **API URL** with anything but an `http` or `https` scheme, a host and a path (no credentials, query or fragment). |
| Transcription model | `dictation.model` | `gpt-live-transcribe` | `gpt-live-transcribe` shows text while the user speaks. OpenAI has deprecated its earlier transcription models (`gpt-4o-transcribe`, `gpt-4o-mini-transcribe` and `whisper-1`), so don't use them for new setups. |
| Language | `dictation.language` | Empty (detected) | An ISO-639-1 code such as `en` or `de` for the language users speak. |

Example configuration, set through `PUT /plugins/mattermost-ai/admin/config` (see [Back up and automate](backup_and_automation.md)):

```json
"dictation": {
  "enabled": true,
  "serviceID": "SERVICE_ID",
  "model": "gpt-live-transcribe",
  "language": "en"
}
```

If the transcription service is later deleted, changed to another type, or the license drops below Enterprise, the button disappears and new sessions are refused. Mattermost never falls back to another service.

## Network requirements

The audio goes from each user's browser or desktop app to OpenAI, so the network between users and OpenAI matters, not only the server's.

- The Mattermost Server needs HTTPS and secure WebSocket access to the OpenAI API host of the service (`api.openai.com` unless the service sets an **API URL**). It opens each session and keeps its control connection.
- Browsers need WebRTC media traffic to OpenAI, over UDP or a TCP fallback. They don't contact the OpenAI API host themselves.
- Networks where only the Mattermost Server can reach the internet can't use dictation. A proxy for the server's own traffic doesn't apply to browsers' media.
- A reverse proxy that adds a `Permissions-Policy` with `microphone=()` blocks dictation. Allow `microphone=(self)` for the Mattermost origin. Mattermost doesn't set that header itself.
- Mattermost must be served over HTTPS (or `localhost`), because browsers only allow microphone access on secure pages.

## Privacy

- Audio goes from the user's device directly to OpenAI and is processed under your organization's agreement with OpenAI, under the API key of the transcription service.
- Mattermost and the plugin don't store audio. Dictated text goes into the message box like typed text, so until the user sends or deletes it, it can be saved as a draft, on the server too when server-synced drafts are on (`ServiceSettings.AllowSyncedDrafts`, the default).
- The OpenAI API key stays on the server; browsers never receive a provider credential.
- Starting a dictation session is recorded in the server audit log (`createDictationSession`) with the transcription service ID. See [Audit logging](observability.md#audit-logging).

## Cost and limits

OpenAI bills transcription to the transcription service's API key. The plugin opens every session and controls it:

- Each user can start dictation at most 10 times a minute, failed starts included, and can have at most 2 sessions running at a time across all servers. Over either limit, starting dictation fails until a slot frees up.
- The plugin ends every session after 10 minutes.
- The plugin ends a session whose settings a client changed: its transcription model, language, prompt, turn detection or session type. A modified client can't switch to a pricier model on your key without the session ending.
- The browser also stops dictating after 30 seconds without speech.

So one user can run at most 2 transcription streams at a time, each for at most 10 minutes. Use a dedicated API key for dictation so you can watch and limit its usage on its own. Each session start is audited (`createDictationSession`), and the server log records each one with the user, the service and the model.

Dictation isn't included in [token usage logging](observability.md#token-usage-log), because the server never sees the transcription usage. Use OpenAI's usage dashboard for the service's API key, or a dedicated key for dictation.

## Limitations

- Only OpenAI services can transcribe. Azure OpenAI and other providers aren't supported yet.
- Mobile apps don't show the button.
- The button is in the message formatting bar. Users who hide the formatting bar don't see it.

## Troubleshooting

| Symptom | Check |
|---|---|
| No microphone button | Dictation is on, the transcription service is a saved OpenAI service, the license is Enterprise or above, the user isn't a guest and has access to the service, and the formatting bar is shown. |
| "Microphone access is blocked" | The user denied the browser's permission prompt, or a `Permissions-Policy` header blocks the microphone. |
| "The speech service could not be reached" | The server couldn't open the session with OpenAI, or the browser can't reach OpenAI over WebRTC. See [Network requirements](#network-requirements). The server log has the reason when OpenAI refused the session. |
| "Dictation can't start right now" | The user started dictation 10 times in the last minute, or already has 2 sessions running, for example in another window. Stopping dictation there, or waiting a minute, clears it. |
| "Dictation stopped at its time limit" | The session reached the 10-minute limit. Starting again opens a new session. |
| "Dictation stopped because its session settings were changed" | Something changed the running session's settings, which only a modified client does. The plugin ended the session. |
| "Dictation stopped because the Agents plugin restarted" | The plugin was disabled, upgraded or restarted while the user was dictating. |
| "You don't have access to dictation" | The user lacks access to the transcription service, or the license is below Enterprise. |

# Set up dictation

Let users dictate messages with a microphone button in every Mattermost message box. Browsers stream audio directly to OpenAI, which transcribes it while the user speaks.

Dictation needs an Enterprise license or above. It uses its own transcription service, chosen separately from the services your agents use, so a server can run agents on Anthropic and dictation on OpenAI. Users don't need an agent to dictate.

## How it works

1. A user selects the microphone button and allows microphone access.
2. The browser asks the plugin for a dictation session. The plugin checks the license, the settings, and the user's access to the service, then asks OpenAI for a short-lived browser key with the service's API key. The key expires 15 seconds after it is issued. That only limits how long the browser has to connect: a session that has connected keeps running after its key expires.
3. The browser connects to OpenAI over WebRTC with that key and streams the microphone audio.
4. OpenAI sends the transcript back to the browser, which types it into the message box at the cursor.

The Mattermost Server never receives audio or transcript text. It sees one request each time a user starts dictating.

## Set it up

1. Add an OpenAI service under **System Console > Plugins > Agents > AI Services** and save, if you don't have one. The service doesn't need an agent. See [Configure LLM services](llm_services.md).
2. In the **Dictation** panel, set **Enable dictation** to **True**.
3. Select the **Transcription service**. Only saved OpenAI services are listed.
4. Check the **Transcription model** and optionally set a **Language**, then save.

An access policy on the transcription service applies to dictation too: users it denies don't see the button. See [Policies for each resource](access_control.md#policies-for-each-resource).

| Setting | Config key | Default | Details |
|---|---|---|---|
| Enable dictation | `dictation.enabled` | Off | Shows the microphone button to users who can use the transcription service. Can't be turned on below Enterprise. |
| Transcription service | `dictation.serviceID` | None | The ID of an OpenAI service. Saving with dictation on fails when the service is missing or isn't an OpenAI service. |
| Transcription model | `dictation.model` | `gpt-live-transcribe` | `gpt-live-transcribe` shows text while the user speaks. `gpt-4o-transcribe` and `gpt-4o-mini-transcribe` show text after each pause and cost less. |
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

Each user's browser or desktop app connects to OpenAI itself, so the network between users and OpenAI matters, not only the server's.

- Browsers need HTTPS access to the OpenAI API host of the service (`api.openai.com` unless the service sets an **API URL**) and WebRTC media traffic to OpenAI, over UDP or a TCP fallback.
- Networks where only the Mattermost Server can reach the internet can't use dictation. A proxy for the server's own traffic doesn't apply to browsers.
- A reverse proxy that adds a `Content-Security-Policy` with `connect-src`, or a `Permissions-Policy` with `microphone=()`, blocks dictation. Allow the OpenAI API host in `connect-src` and `microphone=(self)` for the Mattermost origin. Mattermost doesn't set either header itself.
- Mattermost must be served over HTTPS (or `localhost`), because browsers only allow microphone access on secure pages.

## Privacy

- Audio goes from the user's device directly to OpenAI and is processed under your organization's agreement with OpenAI, under the API key of the transcription service.
- Mattermost and the plugin don't store audio or transcripts. The only text kept is what the user sends as a message.
- The browser key is never logged or kept in audit records or traces.
- Starting a dictation session is recorded in the server audit log (`createDictationSession`) with the service ID and model. See [Audit logging](observability.md#audit-logging).

## Cost and limits

OpenAI bills transcription to the transcription service's API key. Once a session has started, the server can't end it, so cost is bounded by:

- A per-user limit on how often sessions can be started. One key can open more than one session while it is valid, so the limit counts session starts, not open sessions.
- The browser stops dictating after 30 seconds without speech and after 10 minutes in any case.
- OpenAI ends every session after 60 minutes. A browser that ignores the limits above, such as a modified client, can keep a session open that long.

Dictation isn't included in [token usage logging](observability.md#token-usage-log), because the server never sees the transcription usage. Use OpenAI's usage dashboard for the service's API key, or a dedicated key for dictation.

## Limitations

- Only OpenAI services can transcribe. Azure OpenAI and other providers aren't supported yet.
- Mobile apps don't show the button.
- The button is in the message formatting bar. Users who hide the formatting bar don't see it.

## Troubleshooting

| Symptom | Check |
|---|---|
| No microphone button | Dictation is on, the transcription service is a saved OpenAI service, the license is Enterprise or above, the user has access to the service, and the formatting bar is shown. Users may need to reload after you turn dictation on. |
| "Microphone access is blocked" | The user denied the browser's permission prompt, or a `Permissions-Policy` header blocks the microphone. |
| "The speech service could not be reached" | The browser can't reach OpenAI over HTTPS or WebRTC, or a `Content-Security-Policy` blocks it. See [Network requirements](#network-requirements). The server log has the reason when OpenAI refused to create the session. |
| "Dictation was started too often" | The user hit the per-user limit on starting sessions. Wait and try again. |
| "You don't have access to dictation" | The user lacks access to the transcription service, or the license is below Enterprise. |

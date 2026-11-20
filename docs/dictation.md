# Set up dictation

Let users dictate messages with a microphone button in every Mattermost message box. Audio streams from the browser to OpenAI, which transcribes it while the user speaks.

Dictation is experimental and needs an Enterprise license or above. It uses its own transcription service, chosen separately from the services your agents use, so a server can run agents on Anthropic and dictation on OpenAI. Users don't need an agent to dictate.

## How it works

1. A user selects the microphone button and allows microphone access.
2. The browser sends its WebRTC connection offer to the plugin. The plugin checks the license, the settings, and the user's access, then creates the transcription session at OpenAI with the service's API key and the session settings from **Dictation**. The browser never receives an API key or a token.
3. Before answering the browser, the plugin opens its own control connection to the session. The browser then connects to OpenAI over WebRTC and streams the microphone audio.
4. OpenAI sends the transcript back to the browser, which types it into the message box at the cursor.

The plugin ends a session when:

- the session's settings change away from the configured ones (transcription model, language, prompt or keywords, turn detection, or session type), for example because a modified client tried to change them;
- it has run for 10 minutes;
- the plugin is stopped or restarted.

The user sees that dictation stopped. When the user stops dictating, the browser asks the plugin to end the session, and the plugin hangs it up at OpenAI. If the plugin's control connection drops for any reason, the plugin hangs up the session too rather than leaving it unwatched.

## Set it up

1. Open **Agents** from the product switcher and select **Settings**. On **AI services**, add an OpenAI service with an API key and save, if you don't have one. The service doesn't need an agent. See [Configure LLM services](llm_services.md).
2. On **Dictation**, set **Enable dictation** to **True**.
3. Select the **Transcription service**. Only saved OpenAI services are listed.
4. Check the **Transcription model** and optionally set a **Language**, then save.

| Setting | Config key | Default | Details |
|---|---|---|---|
| Enable dictation (experimental) | `dictation.enabled` | Off | Shows the microphone button to users who can dictate. Can't be turned on below Enterprise. |
| Transcription service | `dictation.serviceID` | None | The ID of an OpenAI service. Saving with dictation on fails when the service is missing, isn't an OpenAI service, has no API key, or has an **API URL** that isn't an absolute `http` or `https` URL with only a host and path (no credentials, query or fragment). |
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

## Who can dictate

Every active user who isn't a guest can dictate once it's enabled. Guest accounts, bots and deactivated users can't.

To restrict dictation further, attach an access policy to the transcription service. A policy applies to dictation and to agents on that service. Service access policies need an Enterprise Advanced license with attribute-based access control enabled. On Enterprise, every non-guest user can dictate. See [Policies for each resource](access_control.md#policies-for-each-resource).

Users who can't dictate don't see the button. They aren't told whether the cause is their access or the configuration.

## Network requirements

The plugin talks to OpenAI to create and end sessions, and each user's browser or desktop app sends audio to OpenAI itself.

- The Mattermost Server needs HTTPS and WebSocket (`wss`) access to the service's API host (`api.openai.com` unless the service sets an **API URL**). It uses the server's proxy settings (`HTTPS_PROXY`). The plugin never connects to link-local, cloud metadata (such as `169.254.169.254`, `fd00:ec2::254` and `100.100.100.200`), multicast or unspecified addresses, whatever the API host resolves to. Behind an outbound proxy, the proxy resolves the host itself, so block cloud metadata endpoints at the proxy too.
- Browsers need WebRTC media traffic to OpenAI, over UDP or a TCP fallback. Networks where only the Mattermost Server can reach the internet can't use dictation.
- A reverse proxy that adds a `Permissions-Policy` with `microphone=()` blocks dictation. Allow `microphone=(self)` for the Mattermost origin. Mattermost doesn't set this header itself.
- Mattermost must be served over HTTPS (or `localhost`), because browsers only allow microphone access on secure pages.

## Privacy

- Audio goes from the user's device directly to OpenAI and is processed under your organization's agreement with OpenAI, under the API key of the transcription service. Audio doesn't pass through the Mattermost Server.
- The plugin's control connection receives the session's events, including the transcript, so it can enforce the session settings. The plugin doesn't log, store or audit that content.
- Mattermost and the plugin don't store audio. Dictated text goes into the message box like typed text, so until the user sends or deletes it, it can be saved as a draft, on the server too when server-synced drafts are on (`ServiceSettings.AllowSyncedDrafts`, the default).
- Starting a dictation session is recorded in the server audit log (`createDictationSession`) with the transcription service ID. See [Audit logging](observability.md#audit-logging).

## Cost and limits

OpenAI bills transcription to the transcription service's API key.

- Each user can have at most 2 dictation sessions at a time, across every server in a high-availability cluster.
- Each user can start at most 10 sessions in any 60 seconds on each server. Over either limit, starting dictation fails until a session ends or a minute passes.
- The plugin ends each session after 10 minutes, and as soon as its control connection to OpenAI stops responding (within about a minute). The browser also stops after 30 seconds without speech.
- A session's slot is freed once the plugin has hung it up. If OpenAI doesn't confirm the hangup after a few retries, or a server stops without ending its sessions, the slot stays taken for up to 11 minutes, and OpenAI may keep such a session running for up to its 60-minute maximum.

Use a dedicated API key for dictation with a spending limit at OpenAI. Each session start is audited (`createDictationSession`), and the server log records each session with the user, the service and the configured model.

Dictation isn't included in [token usage logging](observability.md#token-usage-log). Use OpenAI's usage dashboard for the service's API key, or a dedicated key for dictation.

## Limitations

- Only OpenAI services can transcribe. Azure OpenAI and other providers aren't supported yet.
- Mobile apps don't show the button.
- The button is in the message formatting bar. Users who hide the formatting bar don't see it.

## Troubleshooting

| Symptom | Check |
|---|---|
| No microphone button | Dictation is on, the transcription service is a saved OpenAI service, the license is Enterprise or above, the user isn't a guest and has access to the service, and the formatting bar is shown. |
| "Microphone access is blocked" | The user denied the browser's permission prompt, or a `Permissions-Policy` header blocks the microphone. |
| "The speech service could not be reached" | The server log has the reason when OpenAI refused to create the session or the control connection couldn't be opened. Otherwise the browser can't reach OpenAI over WebRTC. See [Network requirements](#network-requirements). |
| "Dictation was started too often" | The user already has 2 sessions, or started 10 in the last 60 seconds. Ending a session or waiting a minute clears it. |
| Dictation stops by itself | The session reached 10 minutes, its settings were changed, or the plugin restarted. The server log records which. |

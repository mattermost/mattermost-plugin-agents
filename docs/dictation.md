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

1. Open **Agents** from the product switcher and select **Settings**. On **AI services**, add an OpenAI service with an API key and save, if you don't have one. The service doesn't need an agent. See [Configure LLM services](llm_services.md).
2. On **Dictation**, set **Enable dictation** to **True**.
3. Select the **Transcription service**. Only saved OpenAI services are listed.
4. Check the **Transcription model** and optionally set a **Language**, then save.

Guest accounts and bot accounts can't dictate. An access policy on the transcription service applies to dictation too: users it denies don't see the button. Service access policies need Enterprise Advanced; without one, every other signed-in user can dictate. See [Policies for each resource](access_control.md#policies-for-each-resource).

| Setting | Config key | Default | Details |
|---|---|---|---|
| Enable dictation | `dictation.enabled` | Off | Shows the microphone button to users who can use the transcription service. Can't be turned on below Enterprise. |
| Transcription service | `dictation.serviceID` | None | The ID of an OpenAI service. Saving with dictation on fails when the service is missing, isn't an OpenAI service, has no API key, or has an **API URL** with anything but a scheme, host and path (no credentials, query or fragment). Browsers receive a URL derived from the API URL. |
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

Each user's browser or desktop app connects to OpenAI itself, so the network between users and OpenAI matters, not only the server's.

- Browsers need HTTPS access to the OpenAI API host of the service (`api.openai.com` unless the service sets an **API URL**) and WebRTC media traffic to OpenAI, over UDP or a TCP fallback.
- Networks where only the Mattermost Server can reach the internet can't use dictation. A proxy for the server's own traffic doesn't apply to browsers.
- A reverse proxy that adds a `Content-Security-Policy` with `connect-src`, or a `Permissions-Policy` with `microphone=()`, blocks dictation. Allow the OpenAI API host in `connect-src` and `microphone=(self)` for the Mattermost origin. Mattermost doesn't set either header itself.
- Mattermost must be served over HTTPS (or `localhost`), because browsers only allow microphone access on secure pages.

## Privacy

- Audio goes from the user's device directly to OpenAI and is processed under your organization's agreement with OpenAI, under the API key of the transcription service.
- Mattermost and the plugin don't store audio. Dictated text goes into the message box like typed text, so until the user sends or deletes it, it can be saved as a draft, on the server too when server-synced drafts are on (`ServiceSettings.AllowSyncedDrafts`, the default).
- The browser key is never logged or kept in audit records or traces.
- Starting a dictation session is recorded in the server audit log (`createDictationSession`) with the transcription service ID. See [Audit logging](observability.md#audit-logging).

## Cost and limits

OpenAI bills transcription to the transcription service's API key. The browser receives a provider key, not one locked session, and the server can't end a session once the key is issued:

- Within its 15-second connect window, one key can open any number of concurrent sessions, and each keeps running for up to OpenAI's 60-minute maximum after the key expires.
- A modified client holding a key can change the session's transcription model, possibly to a pricier one, and its turn detection, language and prompt. It can't change the session type, so it can't turn the session into a speech-to-speech one.
- The plugin limits each user to 10 key requests in any 60 seconds, failed ones included, and at most 2 unexpired keys at a time. Over either limit, starting dictation fails until a slot frees up. The limits are kept in memory on each server, so a high-availability cluster multiplies them by its node count. They count keys, not sessions or audio minutes.
- The browser stops dictating after 30 seconds without speech and after 10 minutes in any case. A modified client can ignore these caps.

So an authenticated, licensed user who scripts key requests can run far more transcription than one dictation session, billed to your API key. Bounding audio minutes needs a server relay that holds the key, which this version doesn't have. Use a dedicated API key for dictation with a tight spending limit at OpenAI. Each issued key is audited (`createDictationSession`), and the server log records each one with the user, the service and the configured model, which may not be the model a modified client is billed for.

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
| "The speech service could not be reached" | The browser can't reach OpenAI over HTTPS or WebRTC, or a `Content-Security-Policy` blocks it. See [Network requirements](#network-requirements). The server log has the reason when OpenAI refused to create the session. |
| "Dictation was started too often" | The user requested 10 keys in the last 60 seconds or already holds 2 unexpired keys. Waiting a minute clears it. |
| "You don't have access to dictation" | The user lacks access to the transcription service, or the license is below Enterprise. |

# Notification lab

A reusable e2e harness that shows exactly what every user receives when a post (typically an AI
agent answer) is created or finished: desktop, push, email, and unread/mention state, per user,
per scenario.

```text
Mattermost (+ agents plugin) ── posts ──> notifications ──> desktop   in-browser Notification recorder, one window per user
        │                                                   push      mock push proxy (Smocker sidecar, /api/v1/send_push)
        │                                                   email     SMTP capture (Mailpit sidecar)
        └── aimock (scripted LLM)                           state     channel members / CRT threads over REST, sidebar badge
```

## Run

```bash
cd e2e
# against the default released image
TESTCONTAINERS_HOST_OVERRIDE=localhost npx playwright test tests/notifications --project=chromium --reporter=list

# against the local server checkout (build once, rebuild after server changes)
scripts/build-local-mm-image.sh                      # -> mm-notification-lab:local
MM_IMAGE=mm-notification-lab:local TESTCONTAINERS_HOST_OVERRIDE=localhost \
  npx playwright test tests/notifications --project=chromium --reporter=list
```

The plugin tarball must exist in `dist/` (`make dist-ci`). Each scenario prints its report table
and attaches it to the Playwright report as `received-report-<label>.txt`. Set
`NOTIFICATION_LAB_REPORT_FILE=/path/report.txt` to also append every table (with the test title and
server image) to one file.

## Agent notification matrix

`tests/notifications/agent-*.spec.ts` cover who is notified about an agent response, scenario by
scenario (S1–S12), with the requester, a thread follower (`member`), a `bystander`, a `mentioned`
user and an `observer` who only watches the stream. Shared setup and assertions live in
`matrix.ts`.

| Spec | Scenarios | Server |
| --- | --- | --- |
| `agent-dm.spec.ts` | S1 watch to the end (agents RHS, core RHS, blurred), S2 leave mid-stream, S7 ask-user question | deferred notifications |
| `agent-channel.spec.ts` | S3 watch then leave, S4 watch to the end (core RHS, Threads view), S5 answer @mentions, S8 LLM failure, S9 stop, S10 regenerate | deferred notifications |
| `agent-tools.spec.ts` | S6 tool approval then share, S11 auto-run tool round | deferred notifications |
| `agent-legacy-server.spec.ts` | S12 fallback push to the requester | without deferred notifications |

`startMatrixLab(browser, {users, browsers, requires})` skips the whole file when the server does not
match `requires` (`deferred`: the plugin reports `deferredNotifications` from `GET /ai_bots`, i.e. the
server has `DeliverPostNotifications`; `legacy`: it does not). The deferred specs therefore skip on
the default image; run them with `MM_IMAGE` set to a server build that has the API:

```bash
MM_IMAGE=mm-notification-lab:local NOTIFICATION_LAB_REPORT_FILE=/tmp/matrix.txt TESTCONTAINERS_HOST_OVERRIDE=localhost \
  npx playwright test tests/notifications/agent-dm.spec.ts tests/notifications/agent-channel.spec.ts \
  tests/notifications/agent-tools.spec.ts --project=chromium --reporter=list
TESTCONTAINERS_HOST_OVERRIDE=localhost npx playwright test tests/notifications/agent-legacy-server.spec.ts \
  --project=chromium --reporter=list
```

Helpers in `matrix.ts`: `slowAnswer` / `toolCallFor` / `slowAnswerAfterTool` / `withTitle` (fixtures,
`SLOW_STREAM` ≈ 10 s per answer), `placeElsewhere` (other channel, blurred, away), `startDiscussion`
(channel + thread started by `member`, read by everyone), `receivedAbout` / `expectNotifiedOnce` /
`expectNotNotified` / `expectNothingAtAll` (per-post assertions; emails are attributed by excluding
other posts' texts), `waitForPostState` and `settlePost` (the latter never fails, so a test can
publish its table before asserting a state the product may not reach).

## API

```ts
import {NotificationLab, slowReply} from 'helpers/notification-lab';

const lab = await NotificationLab.start({
    fixtures: slowReply({prompt: 'hello agent', content: 'A long answer ...'}),
    // users: [{key: 'requester'}, {key: 'member', notifyProps: {push: 'all'}}],  // default: requester, member, follower, bystander
    // collapsedThreads: 'always_on', pushContents: 'full', emailContents: 'full',
    // mcp: {...}, enableChannelMentionToolCalling: true, bot: {mcpDynamicToolLoading: false},  // tool scenarios
});
await lab.openBrowsers(browser, ['requester', 'member']);      // logged-in windows with desktop recording

const dm = await lab.botDm('requester');                       // or createChannel / directChannel
await lab.setStatus('requester', 'away');                      // manual status; push/email depend on it
const mark = await lab.mark();                                 // "everything after this"

await lab.user('requester').typeAndSend('hello agent');        // act through the UI ...
await lab.postAs('member', dm.id, 'hi');                       // ... or through REST
await lab.user('requester').openChannel('town-square');        // client-side navigation, stream keeps running
await lab.user('requester').blur();                            // window not focused

const report = await lab.collect(mark, {
    label: 'agent DM, requester left',
    channelId: dm.id,                    // adds unread/mention counts, posts and sidebar badge
    threadRootId: rootPost.id,           // adds CRT thread unread replies/mentions
    until: (r) => r.posts.some((p) => p.message.includes('done')),
    minWaitMs: 4000,                     // explicit settle window when asserting that nothing arrives
});
await lab.publish(testInfo, report);   // console + Playwright attachment
report.users.requester.push;           // PushRecord[]: type message|clear|update_badge, postId, rootId, channelId, message, senderName, badge
report.users.requester.email;          // EmailRecord[]: to, subject, text
report.users.requester.desktop;        // DesktopRecord[]: title, body, tag
report.users.requester.channelUnread;  // {unreadMsgs, unreadMsgsRoot, mentionCount, mentionCountRoot, urgentMentionCount} | null
report.users.requester.thread;         // {isFollowing, replyCount, unreadReplies, unreadMentions, lastReplyAt, lastViewedAt, unread} | null
report.users.requester.ui;             // sidebar {unread, mentionBadge} | null
report.users.requester.viewing;        // ViewingRecord[]: the webapp's "response on screen" reports (POST /post/:id/viewing)
```

| Method | Purpose |
| --- | --- |
| `NotificationLab.start(options)` | Starts Mattermost + plugin, aimock, push proxy, SMTP capture; configures email/push/CRT; provisions users |
| `lab.user(key)` | `LabUser`: `api` (REST, `api.plugin(method, path)` for plugin routes), `deviceId`, `page`, `setStatus`, `focus/blur`, `navigate/openChannel/openDirectMessage/openThread`, `openThreadInRhs`, `openAgentRhs/askAgentInRhs`, `typeAndSend` |
| `lab.openBrowsers(browser, keys)` | One browser context per user, real login, desktop recorder installed |
| `lab.createChannel / directChannel / botDm` | Channels; `postAs`, `followThread`, `markChannelRead`, `markThreadRead` act as a user |
| `lab.stopResponse / regenerateResponse(key, postId)` | The requester's Stop and Regenerate buttons, over REST |
| `lab.deferredNotificationsSupported()` | Whether the plugin detected `DeliverPostNotifications` on the server |
| `lab.setStatus / setNotifyProps / setChannelNotifyProps` | Server-side knobs that decide who is notified |
| `lab.script / addScript` | Replace/extend the mock LLM fixtures (see `script.ts`) |
| `lab.mark()` | Waits for earlier async deliveries, then returns a cursor |
| `lab.snapshot(mark, opts)` | Report right now |
| `lab.collect(mark, opts)` | Polls until quiet (default 3 s without change) and `until`/`minWaitMs` hold |
| `lab.waitForPost(channelId, match)` | Wait for a post by text/author/time (agent posts get their text saved only when streaming ends) |
| `lab.publish(testInfo, report)` | Print and attach the text table (and append it to `NOTIFICATION_LAB_REPORT_FILE`) |

## How each channel is captured

- **Push**: Mattermost's `PushNotificationServer` is the Smocker sidecar. Pushes are only sent to
  sessions with a device id, so every user gets a second (mobile) session with
  `PUT /users/sessions/device` and device id `android_rn-v2:lab-<key>-<rand>`; the push payload's
  `device_id` maps back to the user. The sender's own mobile session receives silent `clear`
  pushes; assert on `type === 'message'`.
- **Email**: `SendEmailNotifications`, SMTP to the Mailpit sidecar, batching off, verification off.
  Core only emails users whose status is not online/dnd. Recipient address maps back to the user.
- **Desktop**: `window.Notification` is replaced in each user's context by a recorder that also
  reports permission `granted`. `focus()`/`blur()` fire the window events the webapp uses
  (`views.browser.focused`, `window.isActive`) and set what `document.hasFocus()` returns, which
  the agents webapp checks before reporting a response as on screen (headless Chromium always
  reports focus otherwise). `lab.openChannel()` etc. navigate without a page
  reload so a running stream and websocket survive.
- **State**: channel counters come from `GET /channels/{id}` and `/members/{uid}`; thread state from
  `GET /users/{uid}/teams/{tid}/threads/{id}`; the sidebar badge from the user's open window.

## Server behaviours that decide who is notified (verified in the lab)

- `push_status` defaults to `online`: push is skipped only while the user is active in that very
  channel (active channel + activity within 20 s) or is dnd/ooo, so an *away* user who still has
  the channel open and active is not pushed.
- Seeing a post (focused window, channel open) sends a silent `clear` push; so does posting.
- The webapp shows desktop notifications unless the window is focused and that channel/thread is
  open, the user is dnd/ooo, or the channel is muted. Away users still get desktop notifications.
- Manual statuses stick; website activity does not flip them online. The lab disables the
  "switch your status back to Online?" prompt (`auto_reset_manual_status=false`).
- Agent posts stream over websocket events; the post's text is stored only when streaming ends,
  so REST shows an empty agent post mid-stream.
- Core caches each channel's member notify props. Adding members right after creating a channel
  can leave that cache without them (the join/add system posts' notifications re-cache it
  concurrently); a thread follower missing from the cache gets the thread email but no desktop or
  push notification. `lab.createChannel` waits and invalidates the cache, so add members through it.

## Scripting the mock LLM (`script.ts`)

`slowReply` (chunk size + per-chunk delay or first-token delay), `instantReply`, `failingReply`
(HTTP error), `truncatedReply` (stream cut after N chunks), `droppedConnectionReply`
(connection dropped after N ms). Tool calls and multi-step flows reuse `helpers/aimock-fixtures`
(`buildToolCallAndTextResponse`, `buildMultiTurnToolSequence`, `buildToolNameAndTextResponse`);
whether a tool call waits for user approval is plugin tool policy, not mock behaviour (see
`tests/tool-activity/activity-approval.spec.ts` and `tests/tool-config/mock-api/ask_user_question.spec.ts`).

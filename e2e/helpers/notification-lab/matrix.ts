import {expect, test} from '@playwright/test';
import type {Browser, TestInfo} from '@playwright/test';

import type {AIMockFixture, AIMockFixtureFile} from '../aimock-fixtures';
import {buildTitleFixture} from '../aimock-fixtures';

import type {LabUserSpec} from './lab_user';
import {NotificationLab} from './notification_lab';
import type {CollectOptions, LabMark, NotificationLabOptions} from './notification_lab';
import type {MmChannel, MmPost} from './rest';
import type {DesktopRecord, EmailRecord, PushRecord, ReceivedReport, UserKey, UserReport} from './types';

/**
 * Shared setup and vocabulary for the agent notification matrix
 * (`tests/notifications/agent-*.spec.ts`): who is in the conversation, where each user is
 * looking, and what each of them received about one agent post.
 */

/**
 * - requester: asks the agent.
 * - member: channel member who follows the thread (wrote its root post).
 * - bystander: channel member who does not follow the thread.
 * - mentioned: channel member the agent's answer @mentions (only where a scenario needs it).
 * - observer: channel member who watches the thread to let the test see the stream's progress;
 *   never follows or is mentioned, so it is never notified.
 */
export type MatrixUserKey = 'requester' | 'member' | 'bystander' | 'mentioned' | 'observer';

export type Requirement = 'deferred' | 'legacy';

export type MatrixLabOptions = Omit<NotificationLabOptions, 'users'> & {
    users: MatrixUserKey[];
    /** Users who get a logged-in browser window (desktop notifications are only captured there). */
    browsers: MatrixUserKey[];
    /**
     * `deferred`: the scenarios need a server with deferred post notifications
     * (`DeliverPostNotifications`); `legacy`: they describe the plugin's fallback on servers without it.
     * The whole file is skipped when the server does not match.
     */
    requires: Requirement;
};

const SKIP_REASON: Record<Requirement, string> = {
    deferred:
        'Server lacks deferred post notifications (DeliverPostNotifications); run with MM_IMAGE set to a server build ' +
        'that has them (e2e/scripts/build-local-mm-image.sh). The legacy-server spec covers this server.',
    legacy:
        'Server supports deferred post notifications; the legacy-server scenarios need a server without ' +
        'DeliverPostNotifications (the default released image).',
};

/**
 * Starts the lab for a matrix spec file and skips every test in it when the server does not
 * match `requires`. Call from `beforeAll`.
 */
export async function startMatrixLab(browser: Browser, options: MatrixLabOptions): Promise<NotificationLab> {
    const {users, browsers, requires, ...labOptions} = options;
    const specs: LabUserSpec[] = users.map((key) => ({key}));
    const lab = await NotificationLab.start({...labOptions, users: specs});
    const supported = await lab.deferredNotificationsSupported().catch(async (error) => {
        await lab.stop();
        throw error;
    });
    if (supported !== (requires === 'deferred')) {
        await lab.stop();
        test.skip(true, SKIP_REASON[requires]);
    }
    await lab.openBrowsers(browser, browsers);
    return lab;
}

// ---- mock LLM scripts ------------------------------------------------------------------------

/** Stream shape for answers the test acts on mid-stream: ~10 s for ~160 characters. */
export const SLOW_STREAM = {chunkSize: 8, latency: 500} as const;

export function withTitle(...fixtures: AIMockFixture[]): AIMockFixtureFile {
    return {fixtures: [buildTitleFixture('Notification matrix'), ...fixtures]};
}

/** The answer to `prompt`, streamed slowly. */
export function slowAnswer(prompt: string, content: string): AIMockFixture {
    return {match: {userMessage: prompt, hasToolResult: false}, response: {content}, ...SLOW_STREAM};
}

/** The model calls one tool for `prompt` (no text), each streamed chunk `latency` ms apart. */
export function toolCallFor(
    prompt: string,
    call: {id: string; name: string; arguments: Record<string, unknown>},
    latency?: number,
): AIMockFixture {
    return {
        match: {userMessage: prompt, hasToolResult: false},
        response: {toolCalls: [call], finishReason: 'tool_calls'},
        ...(latency === undefined ? {} : {latency}),
    };
}

/** The model's slowly streamed answer once the result of tool call `toolCallId` is back. */
export function slowAnswerAfterTool(toolCallId: string, content: string): AIMockFixture {
    return {match: {toolCallId}, response: {content}, ...SLOW_STREAM};
}

// ---- where users are -------------------------------------------------------------------------

/** The user is away from the conversation: another channel open, window in the background, status away. */
export async function placeElsewhere(lab: NotificationLab, key: MatrixUserKey): Promise<void> {
    const user = lab.user(key);
    if (user.page) {
        await user.openChannel('town-square');
        await user.blur();
    }
    await lab.setStatus(key, 'away');
}

/**
 * A channel with a thread `member` started (so member follows it) and every other matrix user as a
 * member. Everyone has read the channel and the thread.
 */
export async function startDiscussion(
    lab: NotificationLab,
    name: string,
): Promise<{channel: MmChannel; root: MmPost}> {
    const keys = [...lab.users.keys()].filter((key) => key !== 'member');
    const channel = await lab.createChannel({name, members: ['member', ...keys]});
    const root = await lab.postAs('member', channel.id, `Thread started by member for ${name}.`);
    for (const key of keys) {
        await lab.markChannelRead(key, channel.id);
    }
    return {channel, root};
}

// ---- reports ---------------------------------------------------------------------------------

/**
 * `lab.collect` then `lab.publish`. `until` is what the scenario expects to arrive: the report is
 * returned (and published) once it holds, or at the timeout when it never does, so the assertions
 * that follow fail against a published table instead of `collect` throwing.
 */
export async function collectAndPublish(
    lab: NotificationLab,
    testInfo: TestInfo,
    mark: LabMark,
    options: CollectOptions,
): Promise<ReceivedReport> {
    const {until, ...rest} = options;
    const timeoutMs = options.timeoutMs ?? 60000;
    const giveUpAt = Date.now() + timeoutMs - 5000;
    const report = await lab.collect(mark, {
        ...rest,
        timeoutMs,
        until: until && ((rep) => until(rep) || Date.now() > giveUpAt),
    });
    await lab.publish(testInfo, report);
    return report;
}

/** A `message` push to `key` about `postId` arrived. */
export function pushedTo(key: UserKey, postId: string): (report: ReceivedReport) => boolean {
    return (report) => report.users[key].push.some((p) => p.type === 'message' && p.postId === postId);
}

// ---- what users received about one post --------------------------------------------------------

/**
 * `soft` assertions record a failure and let the test go on, for the earlier phases of a multi-phase
 * scenario: the later phases still run and publish their reports.
 */
function expectFor(soft = false): typeof expect {
    return soft ? expect.soft : expect;
}

/** Push notifications a user would see (`message`), ignoring silent `clear`/`update_badge`. */
export function messagePushes(user: UserReport): PushRecord[] {
    return user.push.filter((p) => p.type === 'message');
}

export type Received = {desktop: DesktopRecord[]; push: PushRecord[]; email: EmailRecord[]};

/**
 * What `user` received about the agent post `postId`. Desktop notifications are tagged with the
 * post id and pushes carry it. Emails carry no post id, so an email is about the agent post unless
 * it quotes one of the `otherPostTexts` (e.g. the requester's prompt, which notifies thread followers).
 */
export function receivedAbout(user: UserReport, postId: string, otherPostTexts: string[] = []): Received {
    return {
        desktop: user.desktop.filter((d) => d.tag === postId),
        push: messagePushes(user).filter((p) => p.postId === postId),
        email: user.email.filter((e) => !otherPostTexts.some((text) => e.text.includes(text))),
    };
}

/** The user got exactly one desktop notification, one push and `emails` emails about the post, each showing `text`. */
export function expectNotifiedOnce(
    report: ReceivedReport,
    key: UserKey,
    postId: string,
    text: string,
    options: {otherPostTexts?: string[]; emails?: number; soft?: boolean} = {},
): void {
    const expect = expectFor(options.soft);
    const got = receivedAbout(report.users[key], postId, options.otherPostTexts);
    expect(got.desktop.map((d) => d.body), `${key}: one desktop notification showing the final text`).toEqual([
        expect.stringContaining(text),
    ]);
    expect(got.push.map((p) => p.message), `${key}: one push showing the final text`).toEqual([expect.stringContaining(text)]);
    const emails = options.emails ?? 1;
    expect(got.email.map((e) => e.text), `${key}: ${emails} email(s) showing the final text`).toEqual(
        Array.from({length: emails}, () => expect.stringContaining(text)),
    );
}

/** The user got nothing about the post: no desktop notification, push or email. */
export function expectNotNotified(
    report: ReceivedReport,
    key: UserKey,
    postId: string,
    options: {otherPostTexts?: string[]; soft?: boolean} = {},
): void {
    const expect = expectFor(options.soft);
    const got = receivedAbout(report.users[key], postId, options.otherPostTexts);
    expect(got.desktop, `${key}: no desktop notification about the agent post`).toEqual([]);
    expect(got.push, `${key}: no push about the agent post`).toEqual([]);
    expect(got.email, `${key}: no email about the agent post`).toEqual([]);
}

/** The user got nothing at all since the mark (no desktop notification, message push or email about any post). */
export function expectNothingAtAll(report: ReceivedReport, key: UserKey, options: {soft?: boolean} = {}): void {
    const expect = expectFor(options.soft);
    const user = report.users[key];
    expect(user.desktop, `${key}: no desktop notifications`).toEqual([]);
    expect(messagePushes(user), `${key}: no pushes`).toEqual([]);
    expect(user.email, `${key}: no emails`).toEqual([]);
}

/** Polls the post until `predicate` holds (e.g. its final text is saved) and returns it. */
export async function waitForPostState(
    lab: NotificationLab,
    postId: string,
    predicate: (post: MmPost) => boolean,
    timeoutMs = 90000,
): Promise<MmPost> {
    let latest: MmPost | undefined;
    await expect.poll(async () => {
        latest = await lab.admin.get<MmPost>(`/posts/${postId}`);
        return predicate(latest);
    }, {timeout: timeoutMs, intervals: [250, 500, 1000]}).toBe(true);
    return latest!;
}

/**
 * Polls the post until `predicate` holds or `timeoutMs` passes, without failing, and returns the
 * latest post. Lets a test publish its report before asserting a state the product may never reach.
 */
export async function settlePost(
    lab: NotificationLab,
    postId: string,
    predicate: (post: MmPost) => boolean,
    timeoutMs = 60000,
): Promise<MmPost> {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
        const post = await lab.admin.get<MmPost>(`/posts/${postId}`);
        if (predicate(post) || Date.now() > deadline) {
            return post;
        }
        await new Promise((resolve) => setTimeout(resolve, 500));
    }
}

/** The agent post in `channelId` created since `mark` (exists as soon as the agent starts answering). */
export function waitForAgentPost(lab: NotificationLab, channelId: string, mark: LabMark): Promise<MmPost> {
    return lab.waitForPost(channelId, {byBot: true, since: mark.at});
}

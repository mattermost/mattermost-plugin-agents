import fs from 'fs';

import type {Browser, TestInfo} from '@playwright/test';

import type {AIMockContainer} from '../aimock-container';
import {RunAIMockSidecar} from '../aimock-container';
import type {AIMockFixtureFile} from '../aimock-fixtures';
import {AIMOCK_BOT_NAME, RunAIMockContainer} from '../plugincontainer';
import {mattermostImage} from '../mmcontainer';
import type MattermostContainer from '../mmcontainer';

import {LabUser} from './lab_user';
import type {LabUserSpec} from './lab_user';
import {MailCapture, MAIL_CAPTURE_ALIAS, SMTP_PORT} from './mail_capture';
import {PushProxy} from './push_proxy';
import {buildPostAliases, formatReport} from './report';
import {MmRest} from './rest';
import type {MmChannel, MmChannelMember, MmPost, MmUserStatus} from './rest';
import type {
    CapturedEmail,
    CapturedPush,
    ChannelUnreadState,
    PostSummary,
    ReceivedReport,
    ThreadUnreadState,
    UserKey,
    UserReport,
} from './types';

export const DEFAULT_LAB_USERS: LabUserSpec[] = [
    {key: 'requester'},
    {key: 'member'},
    {key: 'follower'},
    {key: 'bystander'},
];

export type NotificationLabOptions = {
    /** Participants to provision. Defaults to requester, member, follower and bystander. */
    users?: LabUserSpec[];
    /** Initial mock LLM fixtures; replace/extend later with `lab.script()` / `lab.addScript()`. */
    fixtures?: AIMockFixtureFile;
    /** Overrides for the plugin service/bot config (see `RunAIMockContainer`). */
    bot?: Partial<Record<string, unknown>>;
    service?: Partial<Record<string, unknown>>;
    /** Plugin MCP config (embedded server, tool policies); default disabled. See `RunAIMockContainer`. */
    mcp?: Record<string, unknown>;
    /** Let the agent call tools when @mentioned in a channel. Default false. */
    enableChannelMentionToolCalling?: boolean;
    /** `ServiceSettings.CollapsedThreads`. Default `always_on`. */
    collapsedThreads?: 'disabled' | 'default_on' | 'default_off' | 'always_on';
    /** `EmailSettings.PushNotificationContents`. Default `full`. */
    pushContents?: 'full' | 'generic' | 'generic_no_channel' | 'id_loaded';
    /** `EmailSettings.EmailNotificationContentsType`. Default `full`. */
    emailContents?: 'full' | 'generic';
};

/** A point in time; `collect()` reports only what was captured after it. */
export type LabMark = {
    at: number;
    push: number;
    email: Set<string>;
    desktop: Map<UserKey, number>;
    viewing: Map<UserKey, number>;
};

export type CollectOptions = {
    /** Heading of the report table and name of the attachment. */
    label?: string;
    /** Channel whose unread/mention state, posts and sidebar badge are included. */
    channelId?: string;
    /** Root post whose CRT thread state (unread replies/mentions) is included. */
    threadRootId?: string;
    /** List posts created since this time (ms) instead of since the mark. */
    postsSince?: number;
    /**
     * Report is final once nothing changed (captures, unread counts, streamed
     * post text) for this long. Default 3000 ms. Raise it to assert absence.
     */
    quietMs?: number;
    /** Always wait at least this long before returning (explicit settle window for "nothing arrives" assertions). */
    minWaitMs?: number;
    /** Give up waiting for quiet / `until` after this long. Default 60000 ms. */
    timeoutMs?: number;
    /**
     * Do not return before this holds, e.g. `(r) => r.users.member.push.length > 0`.
     * Throws when it still does not hold at `timeoutMs`.
     */
    until?: (report: ReceivedReport) => boolean;
};

const POLL_INTERVAL_MS = 500;

function sleep(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * Notification lab: a Mattermost server with the agents plugin, a scripted
 * mock LLM and per-user capture of everything a user would be notified with.
 *
 * Captured channels per user:
 * - desktop: `new Notification()` calls in the user's own browser window
 * - push:    requests to the mock push proxy, attributed via the user's mobile device id
 * - email:   messages in the SMTP capture, attributed via the recipient address
 * - state:   channel/thread unread and mention counters over REST, sidebar badge in the UI
 *
 * Typical use inside a spec (see `tests/notifications/notification-lab-baseline.spec.ts`):
 *
 *     const lab = await NotificationLab.start({fixtures: slowReply({prompt: 'hi', content: '...'})});
 *     await lab.openBrowsers(browser, ['requester']);
 *     const dm = await lab.botDm('requester');
 *     const mark = await lab.mark();
 *     await lab.user('requester').typeAndSend('hi');
 *     const report = await lab.collect(mark, {channelId: dm.id});
 *     await lab.publish(testInfo, report);
 */
export class NotificationLab {
    readonly users = new Map<UserKey, LabUser>();

    private constructor(
        readonly mattermost: MattermostContainer,
        readonly aimock: AIMockContainer,
        readonly push: PushProxy,
        readonly mail: MailCapture,
        /** System admin session. */
        readonly admin: MmRest,
        readonly team: {id: string; name: string},
        /** The agent's bot account (the plugin's default bot, `aimock`). */
        readonly bot: {id: string; username: string},
    ) {}

    static async start(options: NotificationLabOptions = {}): Promise<NotificationLab> {
        const mattermost = await RunAIMockContainer({
            bot: options.bot,
            service: options.service,
            mcp: options.mcp,
            enableChannelMentionToolCalling: options.enableChannelMentionToolCalling,
        });
        const push = new PushProxy();
        const mail = new MailCapture();
        let aimock: Awaited<ReturnType<typeof RunAIMockSidecar>> | undefined;
        try {
            aimock = await RunAIMockSidecar(mattermost.network, {fixtures: options.fixtures ?? {fixtures: []}});
            await push.start(mattermost.network);
            await mail.start(mattermost.network);

            const admin = new MmRest(mattermost.url());
            await admin.login(mattermost.username, mattermost.password);
            const team = await admin.get<{id: string; name: string}>(`/teams/name/${mattermost.teamName}`);
            const botUser = await admin.get<{id: string; username: string}>(`/users/username/${AIMOCK_BOT_NAME}`);

            const lab = new NotificationLab(mattermost, aimock, push, mail, admin, team, botUser);
            await lab.configureServer(options);
            for (const spec of options.users ?? DEFAULT_LAB_USERS) {
                await lab.provisionUser(spec);
            }
            return lab;
        } catch (error) {
            const started = aimock;
            const stops: Array<() => Promise<unknown>> = [
                () => push.stop(),
                () => mail.stop(),
                () => started?.stop() ?? Promise.resolve(),
                () => mattermost.stop(),
            ];
            for (const stop of stops) {
                await stop().catch(() => undefined);
            }
            throw error;
        }
    }

    async stop(): Promise<void> {
        const stops: Array<() => Promise<unknown>> = [
            ...[...this.users.values()].map((user) => () => user.closeBrowser()),
            () => this.push.stop(),
            () => this.mail.stop(),
            () => this.aimock.stop(),
            () => this.mattermost.stop(),
        ];
        for (const stop of stops) {
            await stop().catch(() => undefined);
        }
    }

    // ---- setup ---------------------------------------------------------------------------

    private async configureServer(options: NotificationLabOptions): Promise<void> {
        await this.admin.put('/config/patch', {
            ServiceSettings: {
                CollapsedThreads: options.collapsedThreads ?? 'always_on',
                EnableUserStatuses: true,
            },
            EmailSettings: {
                SendEmailNotifications: true,
                RequireEmailVerification: false,
                EnableEmailBatching: false,
                EmailNotificationContentsType: options.emailContents ?? 'full',
                SMTPServer: MAIL_CAPTURE_ALIAS,
                SMTPPort: String(SMTP_PORT),
                ConnectionSecurity: '',
                EnableSMTPAuth: false,
                FeedbackName: 'Notification Lab',
                FeedbackEmail: 'notifications@lab.test',
                ReplyToAddress: 'notifications@lab.test',
                SendPushNotifications: true,
                PushNotificationServer: this.push.networkUrl,
                PushNotificationContents: options.pushContents ?? 'full',
            },
        });
    }

    private async provisionUser(spec: LabUserSpec): Promise<LabUser> {
        const username = spec.username ?? spec.key;
        const user = new LabUser(
            spec.key,
            username,
            username,
            `${username}@lab.test`,
            this.mattermost.url(),
            this.team.name,
        );

        const created = await this.admin.post<{id: string}>('/users', {
            email: user.email,
            username,
            password: user.password,
        });
        user.id = created.id;
        await this.admin.post(`/teams/${this.team.id}/members`, {team_id: this.team.id, user_id: user.id});

        await user.api.login(username, user.password);
        await user.api.setSkipOnboarding(user.id);

        if (spec.notifyProps) {
            await this.setNotifyProps(user.key, spec.notifyProps, user);
        }

        if (spec.mobile !== false) {
            const mobile = new MmRest(this.mattermost.url());
            await mobile.login(username, user.password);
            user.deviceId = `android_rn-v2:lab-${spec.key}-${Math.random().toString(36).slice(2, 8)}`;
            await mobile.attachDevice(user.deviceId);
        }

        this.users.set(spec.key, user);
        await user.setStatus(spec.status ?? 'offline');
        return user;
    }

    /** Opens a logged-in browser window (with desktop-notification recording) for each listed user. */
    async openBrowsers(browser: Browser, keys: UserKey[]): Promise<void> {
        for (const key of keys) {
            await this.user(key).openBrowser(browser);
        }
    }

    // ---- accessors and actions -------------------------------------------------------------

    user(key: UserKey): LabUser {
        const user = this.users.get(key);
        if (!user) {
            throw new Error(`Unknown lab user "${key}"; known: ${[...this.users.keys()].join(', ')}`);
        }
        return user;
    }

    /**
     * Sets the user's status (manual, so website activity does not override it).
     * Push and email only go to users who are not online; `away`, `offline` and `dnd` differ in
     * what the server and webapp do with them.
     */
    setStatus(key: UserKey, status: MmUserStatus): Promise<void> {
        return this.user(key).setStatus(status);
    }

    /** Merges `props` into the user's notify props (`desktop`, `push`, `email`, `push_status`, `desktop_threads`, ...). */
    async setNotifyProps(key: UserKey, props: Record<string, string>, user: LabUser = this.user(key)): Promise<void> {
        const current = await this.admin.get<{notify_props: Record<string, string>}>(`/users/${user.id}`);
        await this.admin.put(`/users/${user.id}/patch`, {notify_props: {...current.notify_props, ...props}});
    }

    /** Merges `props` into the user's per-channel notify props (`desktop`, `push`, `mark_unread`, ...). */
    async setChannelNotifyProps(key: UserKey, channelId: string, props: Record<string, string>): Promise<void> {
        const user = this.user(key);
        await user.api.put(`/channels/${channelId}/members/${user.id}/notify_props`, props);
    }

    /** Creates a channel (as the first listed member) and adds the members. */
    async createChannel(options: {name: string; members: UserKey[]; type?: 'O' | 'P'}): Promise<MmChannel> {
        const creator = this.user(options.members[0]);
        const channel = await creator.api.post<MmChannel>('/channels', {
            team_id: this.team.id,
            name: options.name,
            display_name: options.name,
            type: options.type ?? 'O',
        });
        for (const key of options.members.slice(1)) {
            await creator.api.post(`/channels/${channel.id}/members`, {user_id: this.user(key).id});
        }
        // Notifications for the join/add system posts run asynchronously and can re-cache the
        // channel's member notify props without the members added right after. Followers missing
        // from that cache get thread emails but no desktop or push notifications, so wait for those
        // to finish and invalidate the cache with an unchanged channel notify prop.
        await sleep(1500);
        await this.setChannelNotifyProps(options.members[0], channel.id, {mark_unread: 'all'});
        return channel;
    }

    /** Direct message channel between two lab users. */
    directChannel(a: UserKey, b: UserKey): Promise<MmChannel> {
        return this.user(a).api.post<MmChannel>('/channels/direct', [this.user(a).id, this.user(b).id]);
    }

    /** The user's direct message channel with the agent (bot). */
    botDm(key: UserKey): Promise<MmChannel> {
        return this.user(key).api.post<MmChannel>('/channels/direct', [this.user(key).id, this.bot.id]);
    }

    /** Posts through the REST API as `key` (no browser needed). */
    postAs(key: UserKey, channelId: string, message: string, rootId = ''): Promise<MmPost> {
        return this.user(key).api.createPost(channelId, message, rootId);
    }

    /** Makes `key` follow the thread (CRT). Repliers and mentioned users follow automatically. */
    async followThread(key: UserKey, rootPostId: string): Promise<void> {
        const user = this.user(key);
        await user.api.put(`/users/${user.id}/teams/${this.team.id}/threads/${rootPostId}/following`, {});
    }

    /** Marks a channel read for the user, resetting its unread and mention counters. */
    async markChannelRead(key: UserKey, channelId: string): Promise<void> {
        const user = this.user(key);
        await user.api.post(`/channels/members/${user.id}/view`, {channel_id: channelId});
    }

    /** Marks the CRT thread read for the user as of now (what opening the thread does). */
    async markThreadRead(key: UserKey, rootPostId: string): Promise<void> {
        const user = this.user(key);
        await user.api.put(`/users/${user.id}/teams/${this.team.id}/threads/${rootPostId}/read/${Date.now()}`, {});
    }

    /**
     * Whether the server holds agent post notifications until the response ends
     * (`DeliverPostNotifications`), as the agents plugin detected on activation. Servers without it
     * get the plugin's legacy suppression plus a push to the requester at the end.
     */
    async deferredNotificationsSupported(): Promise<boolean> {
        const user = [...this.users.values()][0];
        const response = await user.api.plugin<{deferredNotifications?: boolean}>('GET', '/ai_bots');
        return response.deferredNotifications === true;
    }

    /** Stops a streaming agent response the way the requester's Stop button does. */
    async stopResponse(key: UserKey, postId: string): Promise<void> {
        await this.user(key).api.plugin('POST', `/post/${postId}/stop`);
    }

    /** Regenerates an agent response the way the requester's Regenerate button does. */
    async regenerateResponse(key: UserKey, postId: string): Promise<void> {
        await this.user(key).api.plugin('POST', `/post/${postId}/regenerate`);
    }

    /** Replaces all mock LLM fixtures. */
    script(fixtures: AIMockFixtureFile): Promise<void> {
        return this.aimock.setFixtures(fixtures);
    }

    /** Adds mock LLM fixtures after the existing ones. */
    addScript(fixtures: AIMockFixtureFile): Promise<void> {
        return this.aimock.appendFixtures(fixtures);
    }

    /**
     * Polls the channel until a matching post exists and returns it. A streaming agent post exists
     * (empty) from the moment streaming starts, but its text is only saved when the stream ends;
     * the partial text is visible through the websocket/UI only. So `byBot` + `since` detects
     * "the agent started answering", and `text` detects "the final text was saved".
     */
    async waitForPost(
        channelId: string,
        match: {text?: string; byBot?: boolean; since?: number; timeoutMs?: number} = {},
    ): Promise<MmPost> {
        const deadline = Date.now() + (match.timeoutMs ?? 60000);
        for (;;) {
            const posts = await this.admin.getChannelPosts(channelId);
            const found = posts.find((p) =>
                (match.text === undefined || p.message.includes(match.text)) &&
                (!match.byBot || p.user_id === this.bot.id) &&
                (match.since === undefined || p.create_at >= match.since));
            if (found) {
                return found;
            }
            if (Date.now() > deadline) {
                throw new Error(`No matching post ${JSON.stringify(match)} in channel ${channelId} within timeout`);
            }
            await sleep(POLL_INTERVAL_MS);
        }
    }

    // ---- capture -----------------------------------------------------------------------------

    private async readCaptures(): Promise<{push: CapturedPush[]; email: CapturedEmail[]}> {
        const [push, email] = await Promise.all([this.push.readAll(), this.mail.readAll()]);
        return {push, email};
    }

    private desktopCounts(): Map<UserKey, number> {
        return new Map([...this.users.values()].map((u) => [u.key, u.desktop?.count() ?? 0]));
    }

    private viewingCounts(): Map<UserKey, number> {
        return new Map([...this.users.values()].map((u) => [u.key, u.viewing.length]));
    }

    /**
     * Marks "now". Waits first until previously triggered notifications have
     * arrived (quiet for `quietMs`, default 1500) so they are not attributed
     * to the scenario that follows.
     */
    async mark(options: {quietMs?: number; timeoutMs?: number} = {}): Promise<LabMark> {
        const quietMs = options.quietMs ?? 1500;
        const deadline = Date.now() + (options.timeoutMs ?? 20000);
        let last = '';
        let lastChange = Date.now();
        for (;;) {
            const captures = await this.readCaptures();
            const signature = JSON.stringify([captures.push.length, captures.email.length, [...this.desktopCounts()]]);
            if (signature !== last) {
                last = signature;
                lastChange = Date.now();
            }
            if (Date.now() - lastChange >= quietMs || Date.now() > deadline) {
                return {
                    at: Date.now(),
                    push: captures.push.length,
                    email: new Set(captures.email.map((e) => e.id)),
                    desktop: this.desktopCounts(),
                    viewing: this.viewingCounts(),
                };
            }
            await sleep(POLL_INTERVAL_MS);
        }
    }

    /** One immediate report of everything captured since `mark`, without waiting. */
    async snapshot(mark: LabMark, options: CollectOptions = {}): Promise<ReceivedReport> {
        const label = options.label ?? 'report';
        const captures = await this.readCaptures();
        const newPush = captures.push.slice(mark.push);
        const newEmail = captures.email.filter((e) => !mark.email.has(e.id));

        const byDevice = new Map([...this.users.values()].filter((u) => u.deviceId).map((u) => [u.pushDeviceId, u.key]));
        const byAddress = new Map([...this.users.values()].map((u) => [u.email.toLowerCase(), u.key]));

        const channel = options.channelId ? await this.admin.getChannel(options.channelId) : null;
        const allPosts = options.channelId ? await this.admin.getChannelPosts(options.channelId) : [];
        const aliases = buildPostAliases(allPosts.map((p) => p.id));
        const authors = new Map<string, string>([[this.bot.id, `${this.bot.username} (bot)`]]);
        for (const u of this.users.values()) {
            authors.set(u.id, u.key);
        }
        const posts: PostSummary[] = allPosts
            .filter((p) => p.create_at >= (options.postsSince ?? mark.at))
            .map((p) => ({
                alias: aliases.get(p.id) ?? '?',
                id: p.id,
                author: authors.get(p.user_id) ?? p.user_id,
                rootAlias: p.root_id ? (aliases.get(p.root_id) ?? '?') : '-',
                message: p.message,
                type: p.type,
            }));

        const users: Record<UserKey, UserReport> = {};
        for (const user of this.users.values()) {
            users[user.key] = {
                userKey: user.key,
                username: user.username,
                status: await user.getStatus().catch(() => 'unknown' as const),
                desktop: user.desktop?.since(mark.desktop.get(user.key) ?? 0) ?? [],
                push: newPush.filter((p) => byDevice.get(p.deviceId) === user.key).map((p) => ({...p, userKey: user.key})),
                email: newEmail.filter((e) => e.to.some((to) => byAddress.get(to) === user.key)).map((e) => ({...e, userKey: user.key})),
                channelUnread: channel ? await this.readChannelUnread(user, channel) : null,
                mentionCount: 0,
                thread: options.threadRootId ? await this.readThread(user, options.threadRootId) : null,
                ui: channel ? await user.readSidebarBadge(channel.name) : null,
                viewing: user.viewing.slice(mark.viewing.get(user.key) ?? 0),
            };
            users[user.key].mentionCount = users[user.key].channelUnread?.mentionCount ?? 0;
        }

        const unattributed = {
            push: newPush.filter((p) => !byDevice.has(p.deviceId)),
            email: newEmail.filter((e) => !e.to.some((to) => byAddress.has(to))),
        };
        const userList = [...this.users.keys()].map((key) => users[key]);
        return {
            label,
            users,
            posts,
            unattributed,
            toTable: () => formatReport(label, userList, posts, aliases, {
                push: unattributed.push.length,
                email: unattributed.email.length,
            }),
        };
    }

    private async readChannelUnread(user: LabUser, channel: MmChannel): Promise<ChannelUnreadState | null> {
        const member: MmChannelMember | null = await this.admin.getChannelMember(channel.id, user.id);
        if (!member) {
            return null;
        }
        return {
            channelId: channel.id,
            unreadMsgs: channel.total_msg_count - member.msg_count,
            unreadMsgsRoot: channel.total_msg_count_root - member.msg_count_root,
            mentionCount: member.mention_count,
            mentionCountRoot: member.mention_count_root,
            urgentMentionCount: member.urgent_mention_count,
            notifyProps: {desktop: member.notify_props?.desktop ?? '', push: member.notify_props?.push ?? ''},
        };
    }

    private async readThread(user: LabUser, rootId: string): Promise<ThreadUnreadState> {
        const thread = await user.api.getThread(user.id, this.team.id, rootId);
        const lastReplyAt = thread?.last_reply_at ?? 0;
        const lastViewedAt = thread?.last_viewed_at ?? 0;
        return {
            rootId,
            isFollowing: thread !== null,
            replyCount: thread?.reply_count ?? 0,
            unreadReplies: thread?.unread_replies ?? 0,
            unreadMentions: thread?.unread_mentions ?? 0,
            lastReplyAt,
            lastViewedAt,
            unread: thread !== null && lastReplyAt > lastViewedAt,
        };
    }

    /**
     * Waits for delivery to settle and returns the final report. Polls (no fixed sleeps)
     * until nothing changed for `quietMs`, and at least `minWaitMs` have passed, and
     * `until` (if given) holds. Streamed post text counts as change, so a slow
     * stream is waited out.
     */
    async collect(mark: LabMark, options: CollectOptions = {}): Promise<ReceivedReport> {
        const quietMs = options.quietMs ?? 3000;
        const started = Date.now();
        const deadline = started + (options.timeoutMs ?? 60000);
        let last = '';
        let lastChange = started;
        for (;;) {
            const report = await this.snapshot(mark, options);
            const signature = JSON.stringify([
                report.posts.map((p) => [p.id, p.message.length]),
                Object.values(report.users).map((u) => [
                    u.status, u.desktop.length, u.push.length, u.email.length, u.channelUnread, u.thread,
                ]),
            ]);
            const now = Date.now();
            if (signature !== last) {
                last = signature;
                lastChange = now;
            }

            const quiet = now - lastChange >= quietMs && now - started >= (options.minWaitMs ?? 0);
            const satisfied = options.until ? options.until(report) : true;
            if (quiet && satisfied) {
                return report;
            }
            if (now > deadline) {
                if (!satisfied) {
                    throw new Error(`collect(${options.label ?? ''}) timed out; condition not met:\n${report.toTable()}`);
                }
                return report;
            }
            await sleep(POLL_INTERVAL_MS);
        }
    }

    /**
     * Prints the report table to the test output and attaches it to the Playwright report. With
     * `NOTIFICATION_LAB_REPORT_FILE` set, the table is also appended to that file, so one run
     * produces a single readable record of every scenario.
     */
    async publish(testInfo: TestInfo, report: ReceivedReport): Promise<string> {
        const table = report.toTable();
        console.log(`\n${table}\n`);
        await testInfo.attach(`received-report-${report.label.replace(/[^a-z0-9]+/gi, '-').toLowerCase()}.txt`, {
            body: table,
            contentType: 'text/plain',
        });
        const reportFile = process.env.NOTIFICATION_LAB_REPORT_FILE;
        if (reportFile) {
            const heading = `# ${testInfo.titlePath.slice(1).join(' > ')}  [image: ${mattermostImage()}]`;
            fs.appendFileSync(reportFile, `${heading}\n${table}\n\n`);
        }
        return table;
    }
}

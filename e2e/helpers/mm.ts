import { Page, Locator, Request, expect } from '@playwright/test';
import type { Client4 } from '@mattermost/client';
import type { Post } from '@mattermost/types/posts';
import MattermostContainer from './mmcontainer';
import { showAgentDMAsMessages } from './agent-dm-view';

function getPostsArray(postsResponse: {posts?: Record<string, Post>}): Post[] {
    return Object.values(postsResponse.posts || {});
}

async function fetchPostsForChannel(client: Client4, channelId: string): Promise<Post[]> {
    const getPosts = (client as unknown as { getPostsForChannel?: typeof client.getPosts }).getPostsForChannel
        || client.getPosts;
    if (typeof getPosts !== 'function') {
        throw new Error('Mattermost client does not expose getPostsForChannel or getPosts');
    }
    const postsResponse = await getPosts.call(client, channelId, 0, 200);
    return getPostsArray(postsResponse);
}

const CHUNK_LOAD_ERROR = /Loading (CSS )?chunk \S+ failed/;
const MAX_WEBAPP_RELOADS = 2;
const MAX_LOGIN_ATTEMPTS = 3;

/**
 * Tracks static-asset load failures per page. Chromium aborts in-flight requests with
 * ERR_NETWORK_CHANGED whenever the host's network interfaces change (Docker adding or removing
 * veth devices), and the Mattermost webapp then renders a blank page: either its main bundle never
 * loads, or a lazily loaded chunk rejects and unmounts the React tree. Neither shows up as a
 * missing element until a timeout, so the failure is observed directly.
 */
type WebappLoadWatch = { failed: boolean; waiters: Array<() => void> };

const webappLoadWatches = new WeakMap<Page, WebappLoadWatch>();

function watchWebappLoad(page: Page): WebappLoadWatch {
    const existing = webappLoadWatches.get(page);
    if (existing) {
        return existing;
    }

    const watch: WebappLoadWatch = { failed: false, waiters: [] };
    const markFailed = () => {
        watch.failed = true;
        watch.waiters.splice(0).forEach((resolve) => resolve());
    };
    page.on('pageerror', (error) => {
        if (CHUNK_LOAD_ERROR.test(error.message)) {
            markFailed();
        }
    });
    page.on('requestfailed', (request) => {
        const isStaticAsset = /\/static\/.+\.(js|css)(\?|$)/.test(request.url());
        const errorText = request.failure()?.errorText ?? '';
        // ERR_ABORTED is what a navigation does to requests it cancels, not a load failure.
        if (isStaticAsset && errorText !== 'net::ERR_ABORTED') {
            markFailed();
        }
    });
    webappLoadWatches.set(page, watch);
    return watch;
}

export class MattermostPage {
    readonly page: Page;
    readonly postTextbox: Locator;
    readonly sendButton: Locator;

    constructor(page: Page) {
        this.page = page;
        this.postTextbox = page.getByTestId('post_textbox');
        this.sendButton = page.getByTestId('channel_view').getByTestId('SendMessageButton');
    }

    /**
     * @param options.channelViewTimeoutMs - Max wait after submit for channel URL + channel_view (CI can be slow late in a shard).
     */
    async login(
        url: string,
        username: string,
        password: string,
        options?: { channelViewTimeoutMs?: number },
    ) {
        const channelTimeout = options?.channelViewTimeoutMs ?? 60000;
        watchWebappLoad(this.page);
        await this.page.addInitScript(() => { localStorage.setItem('__landingPageSeen__', 'true'); });

        // Polyfill crypto.randomUUID for insecure contexts (e.g., Docker test environments
        // where the Mattermost URL uses a non-localhost IP like http://172.17.0.1:PORT).
        // crypto.randomUUID requires a secure context but crypto.getRandomValues does not.
        await this.page.addInitScript(() => {
            if (typeof crypto !== 'undefined' && typeof crypto.randomUUID !== 'function') {
                crypto.randomUUID = function randomUUID() {
                    const bytes = new Uint8Array(16);
                    crypto.getRandomValues(bytes);
                    bytes[6] = (bytes[6] & 0x0f) | 0x40;
                    bytes[8] = (bytes[8] & 0x3f) | 0x80;
                    const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
                    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}` as `${string}-${string}-${string}-${string}-${string}`;
                };
            }
        });
        
        // A navigation can fail outright when the browser reports a network change; retry it.
        let lastError: Error | null = null;
        for (let attempt = 0; attempt < 3; attempt++) {
            try {
                await this.page.goto(url, { waitUntil: 'domcontentloaded', timeout: 60000 });
                break;
            } catch (error) {
                lastError = error as Error;
            }
        }
        if (lastError && !(await this.page.getByText('Log in to your account').isVisible().catch(() => false))) {
            throw lastError;
        }

        await this.submitLoginForm(username, password, channelTimeout);
        await this.waitVisibleOrReloadOnLoadFailure(this.page.getByTestId('channel_view'), channelTimeout);
    }

    /**
     * Submits the login form. When the browser aborts the login request itself (ERR_NETWORK_CHANGED),
     * the form stays on "Logging in..." until the timeout, so reload and submit again instead.
     */
    private async submitLoginForm(username: string, password: string, timeout: number) {
        for (let attempt = 1; ; attempt++) {
            await this.waitVisibleOrReloadOnLoadFailure(this.page.getByText('Log in to your account'), 60000);
            await this.page.getByPlaceholder('Password').fill(password);
            await this.page.getByPlaceholder("Email or Username").fill(username);

            let onRequestFailed: (request: Request) => void = () => {};
            const loginRequestFailed = new Promise<'login-request-failed'>((resolve) => {
                onRequestFailed = (request) => {
                    if (new URL(request.url()).pathname === '/api/v4/users/login') {
                        resolve('login-request-failed');
                    }
                };
            });
            this.page.on('requestfailed', onRequestFailed);
            try {
                await this.page.getByTestId('saveSetting').click();
                const outcome = await Promise.race([
                    this.page.waitForURL(/.*\/test\/channels\/.*/, { timeout }).then(() => 'logged-in' as const),
                    loginRequestFailed,
                ]);
                if (outcome === 'logged-in') {
                    return;
                }
            } finally {
                this.page.off('requestfailed', onRequestFailed);
            }

            if (attempt >= MAX_LOGIN_ATTEMPTS) {
                throw new Error(`The login request failed ${attempt} times`);
            }
            await this.page.reload({ waitUntil: 'domcontentloaded' });
        }
    }

    /**
     * Waits for `target` to become visible, reloading the page when a webapp static asset failed to
     * load (see {@link watchWebappLoad}) instead of waiting out the full timeout on a blank page.
     * A failure observed while the target is already visible is ignored.
     */
    async waitVisibleOrReloadOnLoadFailure(target: Locator, timeout: number) {
        const watch = watchWebappLoad(this.page);
        const startedAt = Date.now();

        for (let reloads = 0; ; reloads++) {
            const remaining = Math.max(timeout - (Date.now() - startedAt), 30000);
            const loadFailed = new Promise<'load-failed'>((resolve) => {
                if (watch.failed) {
                    resolve('load-failed');
                } else {
                    watch.waiters.push(() => resolve('load-failed'));
                }
            });
            const outcome = await Promise.race([
                target.waitFor({ state: 'visible', timeout: remaining }).then(() => 'visible' as const),
                loadFailed,
            ]);

            if (outcome === 'visible') {
                return;
            }
            if (await target.isVisible()) {
                watch.failed = false;
                return;
            }
            if (reloads >= MAX_WEBAPP_RELOADS) {
                await target.waitFor({ state: 'visible', timeout: remaining });
                return;
            }

            watch.failed = false;
            await this.page.reload({ waitUntil: 'domcontentloaded' });
        }
    }

    /** Waits for the channel header, reloading if the webapp failed to load its assets. */
    async waitForChannelHeader(timeout = 30000) {
        await this.waitVisibleOrReloadOnLoadFailure(
            this.page.locator('[class*="channel-header"], #channelHeaderInfo').first(),
            timeout,
        );
    }

    async sendChannelMessage(message: string) {
        await this.postTextbox.click();
        await this.postTextbox.fill(message);
        await this.sendButton.press('Enter');
    }

    async mentionBot(botName: string, message: string) {
        await this.sendChannelMessage(`@${botName} ${message}`);
    }

    async waitForReply() {
        await expect(this.page.getByText('1 reply')).toBeVisible();
    }

    /**
     * Legacy heuristic: thread UI "reply" copy. Prefer {@link expectBotDmReplyFromApi} /
     * {@link expectNoBotDmReplyFromApi} for agent access tests — they assert on bot posts via API.
     */
    async expectNoReply() {
        await expect(this.page.getByText('reply')).not.toBeVisible();
    }

    /**
     * Resolve DM channel and bot user id for assertions (same channel as createAndNavigateToDMWithBot).
     */
    async getClientAndDmChannelForBot(
        mattermost: MattermostContainer,
        username: string,
        password: string,
        botUsername: string,
    ): Promise<{ client: Client4; channelId: string; botUserId: string }> {
        const userClient = await mattermost.getClient(username, password);
        const me = await userClient.getMe();
        const botUser = await userClient.getUserByUsername(botUsername);
        const channel = await userClient.createDirectChannel([me.id, botUser.id]);
        return { client: userClient, channelId: channel.id, botUserId: botUser.id };
    }

    /**
     * After the user sends a message in the DM (call with sinceMs from just before send), assert the
     * bot never creates a new post for the **entire** observation window. Polls the channel via API
     * until `observeDurationMs` elapses (default matches {@link expectBotDmReplyFromApi} timeout so
     * slow-reply false negatives are unlikely). Fails immediately if a bot post appears.
     */
    async expectNoBotDmReplyFromApi(
        client: Client4,
        channelId: string,
        botUserId: string,
        sinceMs: number,
        options?: { observeDurationMs?: number; pollIntervalMs?: number },
    ): Promise<void> {
        const observeDuration = options?.observeDurationMs ?? 45000;
        const pollInterval = options?.pollIntervalMs ?? 500;
        const skewMs = 5000;
        const deadline = Date.now() + observeDuration;

        await expect.poll(async () => {
            const posts = await fetchPostsForChannel(client, channelId);
            const botPosts = posts.filter(
                (p) => p.user_id === botUserId && p.create_at >= sinceMs - skewMs,
            );
            if (botPosts.length > 0) {
                throw new Error(
                    `Expected no bot reply post, but found ${botPosts.length} bot post(s) after user message (sinceMs=${sinceMs}).`,
                );
            }
            return Date.now() >= deadline;
        }, {
            message: `Observation window of ${observeDuration}ms for bot DM reply did not complete.`,
            timeout: observeDuration + 30000,
            intervals: [pollInterval],
        }).toBe(true);
    }

    /**
     * Poll until the bot has posted a reply inside the given thread whose content contains
     * `expectedText`, and return the matching post. This is a substring match on the post as
     * fetched, so it proves the text has appeared — not that streaming has finished. Pass text
     * that only occurs in the completed reply, or the returned post may still be streaming.
     */
    async expectBotThreadReplyFromApi(
        client: Client4,
        channelId: string,
        botUserId: string,
        rootId: string,
        expectedText: string,
        options?: { timeoutMs?: number },
    ): Promise<Post> {
        const timeout = options?.timeoutMs ?? 45000;
        let match: Post | undefined;
        await expect.poll(async () => {
            const posts = await fetchPostsForChannel(client, channelId);
            match = posts.find(
                (p) => p.user_id === botUserId && p.root_id === rootId && p.message.includes(expectedText),
            );
            return Boolean(match);
        }, {
            message: `Expected bot ${botUserId} to post a fully-streamed reply containing ${JSON.stringify(expectedText)} in thread ${rootId} of channel ${channelId}.`,
            timeout,
            intervals: [500, 1000, 2000],
        }).toBe(true);
        return match!;
    }

    /**
     * After the user sends a message, assert the bot user posts at least one reply in the DM channel.
     */
    async expectBotDmReplyFromApi(
        client: Client4,
        channelId: string,
        botUserId: string,
        sinceMs: number,
        options?: { timeoutMs?: number },
    ): Promise<void> {
        const timeout = options?.timeoutMs ?? 45000;
        const skewMs = 5000;

        await expect.poll(async () => {
            const posts = await fetchPostsForChannel(client, channelId);
            return posts.filter(
                (p) => p.user_id === botUserId && p.create_at >= sinceMs - skewMs,
            ).length;
        }, { timeout, intervals: [500, 1000, 2000] }).toBeGreaterThan(0);
    }

    async sendMessageAsUser(mattermost: any, username: string, password: string, message: string, channelId?: string) {
        // Get client for the specific user
        const userClient = await mattermost.getClient(username, password);

        // Get the current channel ID if not provided
        let targetChannelId = channelId;
        if (!targetChannelId) {
            // Get the default channel (town-square or similar)
            const teams = await userClient.getMyTeams();
            const team = teams[0];
            const channels = await userClient.getMyChannels(team.id);
            const defaultChannel = channels.find(c => c.name === 'town-square') || channels[0];
            targetChannelId = defaultChannel.id;
        }

        // Create the post
        return await userClient.createPost({
            channel_id: targetChannelId,
            message: message
        });
    }

    async markMessageAsUnread(postid: string) {
		await this.page.locator("#post_" + postid).hover();

		// Click on dot menu
		await this.page.getByTestId('PostDotMenu-Button-' + postid).click();

		await this.page.getByText('Mark as Unread').click();
    }

    async goto(team: string, view: string) {
        // Navigate to team and open AI messages view
        if (view === 'messages') {
            // Open the AI RHS messages view
            const appBarIcon = this.page.locator('#app-bar-icon-mattermost-ai');
            await appBarIcon.waitFor({ state: 'visible', timeout: 10000 });

            // Check if RHS is already open
            const rhsContainer = this.page.getByTestId('mattermost-ai-rhs');
            const isRHSVisible = await rhsContainer.isVisible().catch(() => false);

            if (!isRHSVisible) {
                await appBarIcon.click();
                await rhsContainer.waitFor({ state: 'visible', timeout: 10000 });
            }

            await expect(
                this.page.getByTestId('chat-history').or(this.page.getByTestId('new-chat')).first(),
            ).toBeVisible({ timeout: 10000 });
        }
    }

    async createAndNavigateToDMWithBot(mattermost: any, username: string, password: string, botUsername: string) {
        // Get client for the user
        const userClient = await mattermost.getClient(username, password);
        const currentUser = await userClient.getMe();

        // Get the bot user by username
        const botUser = await userClient.getUserByUsername(botUsername);

        // Create or get DM channel
        const channel = await userClient.createDirectChannel([currentUser.id, botUser.id]);

        // Navigate to the DM channel
        const teams = await userClient.getMyTeams();
        const team = teams[0];

        await this.page.goto(`${mattermost.url()}/${team.name}/messages/@${botUsername}`);

        // Callers drive the stock DM (post list and composer), not the agent chat view.
        await showAgentDMAsMessages(this.page, channel.id);
        await expect(this.postTextbox).toBeVisible({ timeout: 30000 });
    }
}

// Legacy function for backward compatibility
export const login = async (page: Page, url: string, username: string, password: string) => {
    const mmPage = new MattermostPage(page);
    await mmPage.login(url, username, password);
};

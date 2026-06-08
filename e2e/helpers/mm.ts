import { Page, Locator, expect } from '@playwright/test';
import type { Client4 } from '@mattermost/client';
import type { Post } from '@mattermost/types/posts';
import MattermostContainer from './mmcontainer';

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
        
        // Retry navigation with exponential backoff for flaky network conditions
        let lastError: Error | null = null;
        for (let attempt = 0; attempt < 3; attempt++) {
            try {
                await this.page.goto(url, { waitUntil: 'domcontentloaded', timeout: 60000 });
                break;
            } catch (error) {
                lastError = error as Error;
                if (attempt < 2) {
                    await this.page.waitForTimeout(1000 * (attempt + 1));
                }
            }
        }
        if (lastError && !(await this.page.getByText('Log in to your account').isVisible().catch(() => false))) {
            throw lastError;
        }

        // Increased timeout for parallel test execution and added retry logic
        await this.page.getByText('Log in to your account').waitFor({ timeout: 60000 });
        await this.page.getByPlaceholder('Password').fill(password);
        await this.page.getByPlaceholder("Email or Username").fill(username);
        await this.page.getByTestId('saveSetting').click();

        // Wait for navigation to complete and channel view to be visible
        // Using a more generous timeout and proper wait strategy for parallel test runs
        await this.page.waitForURL(/.*\/test\/channels\/.*/, { timeout: channelTimeout });
        await this.page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: channelTimeout });
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

        while (Date.now() < deadline) {
            const posts = await fetchPostsForChannel(client, channelId);
            const botPosts = posts.filter(
                (p) => p.user_id === botUserId && p.create_at >= sinceMs - skewMs,
            );
            if (botPosts.length > 0) {
                throw new Error(
                    `Expected no bot reply post, but found ${botPosts.length} bot post(s) after user message (sinceMs=${sinceMs}).`,
                );
            }
            const remaining = deadline - Date.now();
            if (remaining <= 0) {
                break;
            }
            await this.page.waitForTimeout(Math.min(pollInterval, remaining));
        }
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

            // Wait a bit for posts to load
            await this.page.waitForTimeout(500);
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
        await this.page.waitForTimeout(2000);
    }
}

// Legacy function for backward compatibility
export const login = async (page: Page, url: string, username: string, password: string) => {
    const mmPage = new MattermostPage(page);
    await mmPage.login(url, username, password);
};

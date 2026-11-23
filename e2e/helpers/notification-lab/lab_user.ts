import {expect} from '@playwright/test';
import type {Browser, BrowserContext, Page, Request} from '@playwright/test';

import {AIPlugin} from '../ai-plugin';
import {MattermostPage} from '../mm';

import {blurWindow, DesktopRecorder, focusWindow, isWindowActive} from './desktop_recorder';
import {MmRest} from './rest';
import type {MmUserStatus} from './rest';
import type {UserKey, ViewingRecord} from './types';

const VIEWING_ROUTE = /\/plugins\/mattermost-ai\/post\/([a-z0-9]{26})\/viewing$/;

export type LabUserSpec = {
    /** Key used in reports and `lab.user(key)`, e.g. `requester`. */
    key: UserKey;
    /** Mattermost username; defaults to the key. The password is always the username. */
    username?: string;
    /** Give the user a mobile session so the server sends it push notifications. Default true. */
    mobile?: boolean;
    /** Status to put the user in right after provisioning. Default `offline`. */
    status?: MmUserStatus;
    /** Extra user notify props, merged over the server defaults (e.g. `{push: 'all'}`). */
    notifyProps?: Record<string, string>;
};

type WebappWindow = Window & {
    WebappUtils?: {browserHistory: {push: (path: string) => void}};
};

/**
 * One lab participant: a REST session, an optional mobile (push) session and
 * an optional browser window with a desktop-notification recorder.
 */
export class LabUser {
    /** REST session of a plain web login; used to act as the user and to read unread state. */
    readonly api: MmRest;

    /** Device id attached to the mobile session (`<platform>:<id>`), or '' without a mobile session. */
    deviceId = '';

    id = '';
    page: Page | null = null;
    context: BrowserContext | null = null;
    desktop: DesktopRecorder | null = null;

    /** Viewing reports (`POST /plugins/mattermost-ai/post/:id/viewing`) this user's browser sent. */
    readonly viewing: ViewingRecord[] = [];

    constructor(
        readonly key: UserKey,
        readonly username: string,
        readonly password: string,
        readonly email: string,
        private readonly mattermostUrl: string,
        private readonly teamName: string,
    ) {
        this.api = new MmRest(mattermostUrl);
    }

    /** The part of the device id the push proxy receives as `device_id` (platform prefix removed). */
    get pushDeviceId(): string {
        return this.deviceId.slice(this.deviceId.indexOf(':') + 1);
    }

    async setStatus(status: MmUserStatus): Promise<void> {
        await this.api.setStatus(this.id, status);
    }

    async getStatus(): Promise<MmUserStatus | 'unknown'> {
        const result = await this.api.get<{status?: MmUserStatus}>(`/users/${this.id}/status`);
        return result.status ?? 'unknown';
    }

    // ---- browser -------------------------------------------------------------------------

    /**
     * Opens a dedicated browser context for the user, logs in through the real
     * login form and starts recording desktop notifications. Notification
     * permission is reported as granted.
     */
    async openBrowser(browser: Browser): Promise<Page> {
        if (this.page) {
            return this.page;
        }
        this.context = await browser.newContext();
        this.desktop = await DesktopRecorder.install(this.context);
        this.context.on('request', (request) => this.recordViewing(request));
        this.page = await this.context.newPage();
        await new MattermostPage(this.page).login(this.mattermostUrl, this.username, this.password);
        return this.page;
    }

    private recordViewing(request: Request): void {
        const match = request.method() === 'POST' ? VIEWING_ROUTE.exec(new URL(request.url()).pathname) : null;
        if (!match) {
            return;
        }
        let viewing = false;
        try {
            viewing = Boolean((JSON.parse(request.postData() ?? '{}') as {viewing?: boolean}).viewing);
        } catch {
            // A malformed body is recorded as "not viewing"; the server rejects it anyway.
        }
        this.viewing.push({at: Date.now(), postId: match[1], viewing});
    }

    async closeBrowser(): Promise<void> {
        await this.context?.close();
        this.context = null;
        this.page = null;
        this.desktop = null;
    }

    requirePage(): Page {
        if (!this.page) {
            throw new Error(`User ${this.key} has no browser; call lab.openBrowsers() first`);
        }
        return this.page;
    }

    /** Window focused: the webapp suppresses desktop notifications for the channel/thread that is open. */
    async focus(): Promise<void> {
        await focusWindow(this.requirePage());
    }

    /** Window blurred (user is in another app): the webapp notifies even for the open channel. */
    async blur(): Promise<void> {
        await blurWindow(this.requirePage());
    }

    /** The webapp's `window.isActive`; use to assert the focus helpers took effect. */
    async isFocused(): Promise<boolean> {
        return isWindowActive(this.requirePage());
    }

    /**
     * Client-side (no page reload) navigation, so in-flight state such as a
     * streaming post and the websocket survive, like a user clicking around.
     */
    async navigate(path: string): Promise<void> {
        const page = this.requirePage();
        await page.evaluate((target) => {
            (window as WebappWindow).WebappUtils?.browserHistory.push(target);
        }, path);
        await page.waitForURL((url) => url.pathname === path || url.pathname.endsWith(path), {timeout: 15000});
    }

    openChannel(channelName: string): Promise<void> {
        return this.navigate(`/${this.teamName}/channels/${channelName}`);
    }

    openDirectMessage(username: string): Promise<void> {
        return this.navigate(`/${this.teamName}/messages/@${username}`);
    }

    /** Opens a thread in the Followed Threads view (collapsed reply threads). */
    openThread(rootPostId: string): Promise<void> {
        return this.navigate(`/${this.teamName}/threads/${rootPostId}`);
    }

    /**
     * Opens the thread of `rootPostId` in the core right-hand sidebar of the channel that is open
     * (the reply arrow on the root post), the way a user follows a conversation next to the channel.
     */
    async openThreadInRhs(rootPostId: string): Promise<void> {
        const page = this.requirePage();
        const rootPost = page.locator(`#post_${rootPostId}`);
        await expect(rootPost).toBeVisible({timeout: 30000});
        await rootPost.hover();
        await page.locator(`#CENTER_commentIcon_${rootPostId}`).click();
        await expect(page.locator(`#rhsContainer #rhsPostMessageText_${rootPostId}`)).toBeVisible({timeout: 15000});
    }

    /** Opens the agents plugin's right-hand sidebar on a fresh chat. */
    async openAgentRhs(): Promise<AIPlugin> {
        const plugin = new AIPlugin(this.requirePage());
        await plugin.resetState();
        return plugin;
    }

    /** Sends `message` to the agent from the agents plugin's right-hand sidebar (new chat). */
    async askAgentInRhs(message: string): Promise<AIPlugin> {
        const plugin = await this.openAgentRhs();
        await plugin.sendMessage(message);
        return plugin;
    }

    /** Posts through the UI composer of the channel that is currently open in this user's browser. */
    async typeAndSend(message: string): Promise<void> {
        const page = this.requirePage();
        const textbox = page.getByTestId('post_textbox');
        await textbox.click();
        await textbox.fill(message);
        await textbox.press('Enter');
    }

    /**
     * What the sidebar shows for the channel. Null when there is no browser
     * or the channel has no sidebar entry (e.g. it is the open channel's own
     * entry in another team).
     */
    async readSidebarBadge(channelName: string): Promise<{unread: boolean; mentionBadge: number} | null> {
        if (!this.page) {
            return null;
        }
        const item = this.page.locator(`#sidebarItem_${channelName}`);
        if ((await item.count()) === 0) {
            return null;
        }
        const className = (await item.first().getAttribute('class')) ?? '';
        const badge = item.first().locator('.badge');
        const badgeText = (await badge.count()) > 0 ? ((await badge.first().textContent()) ?? '') : '';
        return {
            unread: /unread/.test(className) || badgeText.trim() !== '',
            mentionBadge: Number.parseInt(badgeText, 10) || 0,
        };
    }
}

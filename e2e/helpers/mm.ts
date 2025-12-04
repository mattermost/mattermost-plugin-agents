import { Page, Locator, expect } from '@playwright/test';

export class MattermostPage {
    readonly page: Page;
    readonly postTextbox: Locator;
    readonly sendButton: Locator;

    constructor(page: Page) {
        this.page = page;
        this.postTextbox = page.getByTestId('post_textbox');
        this.sendButton = page.getByTestId('channel_view').getByTestId('SendMessageButton');
    }

    async login(url: string, username: string, password: string) {
        await this.page.addInitScript(() => { localStorage.setItem('__landingPageSeen__', 'true'); });
        
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
        await this.page.waitForURL(/.*\/test\/channels\/.*/, { timeout: 60000 });
        await this.page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 60000});
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

    async expectNoReply() {
        await expect(this.page.getByText('reply')).not.toBeVisible();
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

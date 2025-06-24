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
        await this.page.goto(url);
        await this.page.getByText('Log in to your account').waitFor();
        await this.page.getByPlaceholder('Password').fill(password);
        await this.page.getByPlaceholder("Email or Username").fill(username);
        await this.page.getByTestId('saveSetting').click();
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
}

// Legacy function for backward compatibility
export const login = async (page: Page, url: string, username: string, password: string) => {
    const mmPage = new MattermostPage(page);
    await mmPage.login(url, username, password);
};

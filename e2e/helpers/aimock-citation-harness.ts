import { BrowserContext, Page, expect } from '@playwright/test';

import { AIPlugin } from './ai-plugin';
import { AIMockContainer, RunAIMockSidecar } from './aimock-container';
import { AIMockFixtureFile } from './aimock-fixtures';
import { containerTest, Own } from './container-fixture';
import { LLMBotPostHelper } from './llmbot-post';
import { MattermostPage } from './mm';
import MattermostContainer from './mmcontainer';
import { AIMOCK_BOT_NAME, RunAIMockContainer } from './plugincontainer';
import {
    RunWebSearchMockSidecar,
    WebSearchMockContainer,
    WEB_SEARCH_PLUGIN_CONFIG,
} from './web-search-mock-container';

// Citation suites use deterministic WebSearch tool fallback (useResponsesAPI: false).
export const AIMOCK_CITATION_MODE = 'webSearchTool' as const;

export type AIMockCitationStack = {
    mattermost: MattermostContainer;
    aimock: AIMockContainer;
    webSearchMock: WebSearchMockContainer;
    botUsername: string;
};

export async function startAIMockCitationStack(own: Own, options: {
    fixtures: AIMockFixtureFile;
    reasoningEnabled?: boolean;
}): Promise<AIMockCitationStack> {
    const mattermost = own(await RunAIMockContainer({
        webSearch: WEB_SEARCH_PLUGIN_CONFIG,
        bot: {
            reasoningEnabled: options.reasoningEnabled ?? false,
            enabledNativeTools: [],
        },
    }));
    const webSearchMock = own(await RunWebSearchMockSidecar(mattermost.network));
    const aimock = own(await RunAIMockSidecar(mattermost.network, {
        fixtures: options.fixtures,
    }));

    return {
        mattermost,
        aimock,
        webSearchMock,
        botUsername: AIMOCK_BOT_NAME,
    };
}

// aimock strict fixtures are single-use; give each Playwright case its own stack.
export type AIMockCitationTestContext = {
    page: Page;
    context: BrowserContext;
    aiPlugin: AIPlugin;
    llmBotHelper: LLMBotPostHelper;
};

export function describeAIMockCitationCase(options: {
    title: string;
    fixtures: AIMockFixtureFile;
    reasoningEnabled?: boolean;
    timeoutMs?: number;
    run: (ctx: AIMockCitationTestContext) => Promise<void>;
}): void {
    const test = containerTest((own) => startAIMockCitationStack(own, options));

    test.describe(options.title, () => {
        test(options.title, async ({ page, context, containers: stack }) => {
            test.setTimeout(options.timeoutMs ?? 120000);

            const mmPage = new MattermostPage(page);
            const aiPlugin = new AIPlugin(page);
            const llmBotHelper = new LLMBotPostHelper(page);

            await mmPage.login(stack.mattermost.url(), 'regularuser', 'regularuser', {
                channelViewTimeoutMs: 90000,
            });
            await mmPage.createAndNavigateToDMWithBot(
                stack.mattermost,
                'regularuser',
                'regularuser',
                stack.botUsername,
            );
            await aiPlugin.openRHS();

            await options.run({ page, context, aiPlugin, llmBotHelper });
        });
    });
}

export async function approvePendingWebSearchTool(page: Page): Promise<void> {
    const acceptButton = page.getByRole('button', { name: /^Accept$/i });
    await expect(acceptButton).toBeVisible({ timeout: 120000 });
    await acceptButton.click();
}

export async function sendMessageWithWebSearchApproval(
    page: Page,
    aiPlugin: { sendMessage: (message: string) => Promise<void> },
    message: string,
): Promise<void> {
    await aiPlugin.sendMessage(message);
    await approvePendingWebSearchTool(page);
}

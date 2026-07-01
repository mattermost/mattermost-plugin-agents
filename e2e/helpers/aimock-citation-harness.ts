import { BrowserContext, Page, expect, test } from '@playwright/test';

import { AIPlugin } from './ai-plugin';
import { AIMockContainer, RunAIMockSidecar } from './aimock-container';
import { AIMockFixtureFile } from './aimock-fixtures';
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

export const AIMOCK_BEFORE_ALL_TIMEOUT_MS = 180000;

export type AIMockCitationStack = {
    mattermost: MattermostContainer;
    aimock: AIMockContainer;
    webSearchMock: WebSearchMockContainer;
    botUsername: string;
};

export async function startAIMockCitationStack(options: {
    fixtures: AIMockFixtureFile;
    reasoningEnabled?: boolean;
}): Promise<AIMockCitationStack> {
    let mattermost: MattermostContainer | null = null;
    let webSearchMock: WebSearchMockContainer | null = null;
    let aimock: AIMockContainer | null = null;

    try {
        mattermost = await RunAIMockContainer({
            webSearch: WEB_SEARCH_PLUGIN_CONFIG,
            bot: {
                reasoningEnabled: options.reasoningEnabled ?? false,
                enabledNativeTools: [],
            },
        });

        webSearchMock = await RunWebSearchMockSidecar(mattermost.network);
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: options.fixtures,
        });

        return {
            mattermost,
            aimock,
            webSearchMock,
            botUsername: AIMOCK_BOT_NAME,
        };
    } catch (error) {
        if (aimock) {
            await aimock.stop().catch(() => undefined);
        }
        if (webSearchMock) {
            await webSearchMock.stop().catch(() => undefined);
        }
        if (mattermost) {
            await mattermost.stop().catch(() => undefined);
        }
        throw error;
    }
}

export async function stopAIMockCitationStack(stack: AIMockCitationStack): Promise<void> {
    const errors: unknown[] = [];
    const stops = [
        () => stack.aimock.stop(),
        () => stack.webSearchMock.stop(),
        () => stack.mattermost.stop(),
    ];

    for (const stop of stops) {
        try {
            await stop();
        } catch (error) {
            errors.push(error);
        }
    }

    if (errors.length > 0) {
        throw errors[0];
    }
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
    test.describe(options.title, () => {
        let stack: AIMockCitationStack;

        test.beforeAll(async () => {
            test.setTimeout(AIMOCK_BEFORE_ALL_TIMEOUT_MS);
            stack = await startAIMockCitationStack({
                fixtures: options.fixtures,
                reasoningEnabled: options.reasoningEnabled ?? false,
            });
        });

        test.afterAll(async () => {
            if (stack) {
                await stopAIMockCitationStack(stack);
            }
        });

        test(options.title, async ({ page, context }) => {
            test.setTimeout(options.timeoutMs ?? 120000);

            const mmPage = new MattermostPage(page);
            const aiPlugin = new AIPlugin(page);
            const llmBotHelper = new LLMBotPostHelper(page);

            await mmPage.login(stack.mattermost.url(), 'regularuser', 'regularuser');
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

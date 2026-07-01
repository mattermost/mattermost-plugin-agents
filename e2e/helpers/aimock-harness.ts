import fs from 'fs';
import path from 'path';

import { Page } from '@playwright/test';

import { AIMockContainer, RunAIMockSidecar } from './aimock-container';
import { AIMockFixtureFile } from './aimock-fixtures';
import { AIPlugin } from './ai-plugin';
import { LLMBotPostHelper } from './llmbot-post';
import { MattermostPage } from './mm';
import MattermostContainer from './mmcontainer';
import { RunAIMockContainer } from './plugincontainer';

export type AIMockHarness = {
    mattermost: MattermostContainer;
    aimock: AIMockContainer;
    stop: () => Promise<void>;
};

export function loadAimockFixtureFile(fixtureRelativePath: string): AIMockFixtureFile {
    const fixturePath = path.join(__dirname, '..', 'fixtures', 'aimock', fixtureRelativePath);
    const raw = fs.readFileSync(fixturePath, 'utf-8');
    return JSON.parse(raw) as AIMockFixtureFile;
}

const DEFAULT_AIMOCK_USERNAME = 'regularuser';
const DEFAULT_AIMOCK_PASSWORD = 'regularuser';

export async function setupAimockTestPage(
    page: Page,
    mattermostUrl: string,
    credentials: { username?: string; password?: string } = {},
): Promise<{
    mmPage: MattermostPage;
    aiPlugin: AIPlugin;
    llmBotHelper: LLMBotPostHelper;
}> {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const llmBotHelper = new LLMBotPostHelper(page);

    await mmPage.login(
        mattermostUrl,
        credentials.username ?? DEFAULT_AIMOCK_USERNAME,
        credentials.password ?? DEFAULT_AIMOCK_PASSWORD,
    );
    await aiPlugin.resetState();

    return { mmPage, aiPlugin, llmBotHelper };
}

export async function RunAIMockHarness(options: {
    fixtureFile: string;
    bot?: Partial<Record<string, unknown>>;
    service?: Partial<Record<string, unknown>>;
}): Promise<AIMockHarness> {
    const fixtures = loadAimockFixtureFile(options.fixtureFile);
    const mattermost = await RunAIMockContainer({
        bot: options.bot,
        service: options.service,
    });
    try {
        const aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures,
        });

        return {
            mattermost,
            aimock,
            stop: async () => {
                const errors: unknown[] = [];
                for (const stop of [() => aimock.stop(), () => mattermost.stop()]) {
                    try {
                        await stop();
                    } catch (error) {
                        errors.push(error);
                    }
                }

                if (errors.length > 0) {
                    throw errors[0];
                }
            },
        };
    } catch (error) {
        await mattermost.stop().catch(() => undefined);
        throw error;
    }
}

import { test, expect, type Page } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildToolCallResponse,
    buildTextResponse,
} from 'helpers/openai-mock';
import { RunToolConfigContainerWithServiceAccountBots } from 'helpers/tool-config-container';
import { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Experimental service account agent settings with a mocked LLM.
 *
 * The mock asks for `mattermost__create_post`, which has the default "ask"
 * policy. `sabaseline` must stop for approval; `saexperimental` (skip tool
 * call approvals + bot account permissions) must post without asking, and the
 * post must be authored by its bot account rather than the requesting admin.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;
let townSquare: {id: string; displayName: string; teamDisplayName: string};

const createPostTool = 'mattermost__create_post';
const createPostLabel = 'Create Post';

async function mockCreatePostTurn(botDisplayName: string, botUsername: string, callID: string, message: string, continuation: string) {
    const chatCompletion = (value: string, body: string) => ({
        request: {
            method: 'POST',
            path: '/v1/chat/completions',
            body: {matcher: 'ShouldContainSubstring', value},
        },
        context: {times: 1},
        response: {
            status: 200,
            headers: {'Content-Type': 'text/event-stream'},
            body,
        },
    });

    await openAIMock.addMocks([
        chatCompletion(
            'Write a short title for the following request. Include only the title and nothing else, no quotations. Request:',
            buildTextResponse('Service account post'),
        ),
        // Only the main turn carries the bot system prompt; title generation is user-only.
        chatCompletion(
            `You are called ${botDisplayName} with the username ${botUsername}`,
            buildToolCallResponse(callID, createPostTool, JSON.stringify({
                channel_id: townSquare.id,
                channel_display_name: townSquare.displayName,
                team_display_name: townSquare.teamDisplayName,
                message,
            })),
        ),
        chatCompletion(callID, buildTextResponse(continuation)),
    ]);
}

async function sendDMAndOpenThread(page: Page, botUsername: string, userMessage: string) {
    const mmPage = new MattermostPage(page);
    await mmPage.login(mattermost.url(), adminUsername, adminPassword);
    await mmPage.createAndNavigateToDMWithBot(mattermost, adminUsername, adminPassword, botUsername);
    await mmPage.sendChannelMessage(userMessage);

    const sentPost = page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(userMessage, {exact: true}),
    }).last();
    await expect(sentPost).toBeVisible({timeout: 30000});
    const replyIndicator = sentPost.getByText(/\d+ repl/i);
    await expect(replyIndicator).toBeVisible({timeout: 30000});
    await replyIndicator.click();

    const rhs = page.locator('#rhsContainer');
    await expect(rhs.locator('[data-testid="llm-bot-post"]').first()).toBeVisible({timeout: 10000});
    return rhs;
}

async function townSquarePostsWithMessage(message: string) {
    const adminClient = await mattermost.getAdminClient();
    const posts = await adminClient.getPosts(townSquare.id);
    return Object.values(posts.posts).filter((post) => post.message === message);
}

test.describe('Experimental service account settings (Mocked LLM)', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainerWithServiceAccountBots();
        openAIMock = await RunOpenAIMocks(mattermost.network);

        // Running as its bot account, saexperimental can only post where the bot is a member.
        await mattermost.addUserToTeam('saexperimental', 'test');

        const adminClient = await mattermost.getAdminClient();
        const team = await adminClient.getTeamByName('test');
        const channel = await adminClient.getChannelByName(team.id, 'town-square');
        townSquare = {id: channel.id, displayName: channel.display_name, teamDisplayName: team.display_name};
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('service account agent without the settings waits for approval', async ({ page }) => {
        test.setTimeout(120000);

        const runID = Date.now();
        const postMessage = `sa baseline post ${runID}`;
        const continuation = `SA_BASELINE_CONTINUATION_${runID}`;
        await mockCreatePostTurn('SA Baseline Bot', 'sabaseline', `call_sa_baseline_${runID}`, postMessage, continuation);

        const rhs = await sendDMAndOpenThread(page, 'sabaseline', `baseline request ${runID}`);

        await expect(rhs.getByText(createPostLabel, {exact: true})).toBeVisible({timeout: 45000});
        await expect(rhs.getByRole('button', {name: /^accept$/i})).toBeVisible({timeout: 30000});
        await expect(rhs.getByText(continuation)).not.toBeVisible();
        expect(await townSquarePostsWithMessage(postMessage)).toHaveLength(0);
    });

    test('skip tool call approvals and bot account permissions post as the agent bot without asking', async ({ page }) => {
        test.setTimeout(120000);

        const runID = Date.now();
        const postMessage = `sa experimental post ${runID}`;
        const continuation = `SA_EXPERIMENTAL_CONTINUATION_${runID}`;
        await mockCreatePostTurn('SA Experimental Bot', 'saexperimental', `call_sa_experimental_${runID}`, postMessage, continuation);

        const rhs = await sendDMAndOpenThread(page, 'saexperimental', `experimental request ${runID}`);

        await expect(rhs.getByText(continuation)).toBeVisible({timeout: 45000});
        await expect(rhs.getByText(createPostLabel, {exact: true})).toBeVisible();
        await expect(rhs.getByText('Auto-approved')).toBeVisible();
        await expect(rhs.getByRole('button', {name: /^accept$/i})).not.toBeVisible();

        const adminClient = await mattermost.getAdminClient();
        const botUser = await adminClient.getUserByUsername('saexperimental');
        const created = await townSquarePostsWithMessage(postMessage);
        expect(created).toHaveLength(1);
        expect(created[0].user_id).toBe(botUser.id);
    });
});

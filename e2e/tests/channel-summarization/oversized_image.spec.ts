// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, test} from '@playwright/test';

import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildChatCompletionMockRule,
    buildTextResponse,
    normalizeChatCompletionMockPath,
    titleGenerationMockRule,
} from 'helpers/openai-mock';
import {RunAIMockContainer} from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';
const rootMessage = 'Project status image for thread summarization (2400 × 1 pixels)';
const summaryResponse = 'Thread summary completed. One oversized image was omitted because it exceeds the 2000 pixel limit.';
const omissionContext = 'Image omitted because its dimensions (2400x1)';
const oversizedPNG = Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAACWAAAAABCAIAAADVHwIVAAAAIklEQVR42u3CAQ0AAAgDIJs8osEtYZDDmOypqqqqqqqqJR8RsM8kAgyuYwAAAABJRU5ErkJggg==',
    'base64',
);

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
    mattermost = await RunAIMockContainer({
        bot: {
            enableVision: true,
        },
    });
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.afterAll(async () => {
    await openAIMock.stop();
    await mattermost.stop();
});

test('summarizes a channel thread after omitting an oversized image', async ({page}) => {
    await openAIMock.addMocks([
        buildChatCompletionMockRule(buildTextResponse(summaryResponse), {
            bodyContains: omissionContext,
        }),
        normalizeChatCompletionMockPath({
            request: {
                method: 'POST',
                path: '/v1/chat/completions',
                body: {
                    matcher: 'ShouldContainSubstring',
                    value: 'data:image/',
                },
            },
            context: {times: 100},
            response: {
                status: 400,
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({
                    error: {
                        message: 'At least one image dimension exceeds 2000 pixels',
                        type: 'invalid_request_error',
                    },
                }),
            },
        }),
        titleGenerationMockRule(),
    ]);

    const userClient = await mattermost.getClient(username, password);
    const team = await userClient.getTeamByName('test');
    const channel = await userClient.getChannelByName(team.id, 'town-square');
    const form = new FormData();
    form.append('channel_id', channel.id);
    form.append('files', new Blob([oversizedPNG], {type: 'image/png'}), 'oversized.png');
    const upload = await userClient.uploadFile(form);
    await userClient.createPost({
        channel_id: channel.id,
        message: rootMessage,
        file_ids: [upload.file_infos[0].id],
    });

    const mmPage = new MattermostPage(page);
    await mmPage.login(mattermost.url(), username, password);
    await page.goto(`${mattermost.url()}/test/channels/town-square`);

    const rootPost = page.getByText(rootMessage, {exact: true});
    await expect(rootPost).toBeVisible();
    await rootPost.hover();
    await page.getByRole('button', {name: 'reply'}).click();

    const thread = page.getByRole('region', {name: 'Thread Town Square'});
    await thread.getByRole('textbox', {name: 'Reply to this thread...'}).fill('@aimock Please summarize this thread.');
    await thread.getByRole('button', {name: 'Send Now'}).click();

    await expect(thread.getByText(summaryResponse, {exact: true})).toBeVisible({timeout: 30000});
    await expect(thread.getByText(/Sorry! An error occurred while accessing the LLM/)).not.toBeVisible();

    const historyResponse = await fetch(
        `http://localhost:${openAIMock.container.getMappedPort(8081)}/history`,
    );
    expect(historyResponse.ok).toBe(true);
    const history = await historyResponse.json() as Array<{request?: {body?: unknown}}>;
    const requestBodies = history.map((entry) => JSON.stringify(entry.request?.body ?? ''));
    expect(requestBodies.some((body) => body.includes(omissionContext))).toBe(true);
    expect(requestBodies.every((body) => !body.includes('data:image/'))).toBe(true);
});

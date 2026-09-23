// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {deflateSync} from 'node:zlib';

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
// OpenAI's documented hard side cap is 65535px; a 1-pixel-wider image is omitted.
const openaiMaxImageDimension = 65535;
const oversizedWidth = openaiMaxImageDimension + 1;
const rootMessage = `Project status image for thread summarization (${oversizedWidth} × 1 pixels)`;
const summaryResponse = `Thread summary completed. One oversized image was omitted because it exceeds the ${openaiMaxImageDimension} pixel limit.`;
const omissionContext = `Image omitted because its dimensions (${oversizedWidth}x1)`;
const oversizedPNG = grayscalePNG(oversizedWidth, 1);

function crc32(data: Buffer): number {
    let crc = 0xffffffff;
    for (const byte of data) {
        crc ^= byte;
        for (let i = 0; i < 8; i++) {
            crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
        }
    }
    return (crc ^ 0xffffffff) >>> 0;
}

function pngChunk(type: string, data: Buffer): Buffer {
    const typeBuf = Buffer.from(type, 'ascii');
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(crc32(Buffer.concat([typeBuf, data])));
    return Buffer.concat([len, typeBuf, data, crc]);
}

function grayscalePNG(width: number, height: number): Buffer {
    const raw = Buffer.alloc((width + 1) * height, 0xff);
    for (let y = 0; y < height; y++) {
        raw[y * (width + 1)] = 0;
    }
    const ihdr = Buffer.alloc(13);
    ihdr.writeUInt32BE(width, 0);
    ihdr.writeUInt32BE(height, 4);
    ihdr[8] = 8;
    return Buffer.concat([
        Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
        pngChunk('IHDR', ihdr),
        pngChunk('IDAT', deflateSync(raw)),
        pngChunk('IEND', Buffer.alloc(0)),
    ]);
}

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
                        message: `At least one image dimension exceeds ${openaiMaxImageDimension} pixels`,
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

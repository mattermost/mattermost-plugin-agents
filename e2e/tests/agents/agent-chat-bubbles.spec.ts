// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Agent chat bubbles in the Agents product view: short replies hug their
// content, wide ones span the row, and a streamed reply grows without
// re-creating text that is already on screen.

import {expect, type Locator, type Page} from '@playwright/test';

import {AgentChatPage} from 'helpers/agent-chat-page';
import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {containerTest} from 'helpers/container-fixture';
import {withDeadline} from 'helpers/deadline';
import {buildReasoningResponse, buildTextResponse} from 'helpers/aimock-fixtures';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {AIMOCK_BOT_NAME, RunAIMockContainer} from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';

// A hugging bubble is clearly narrower than the row; a full one spans it.
const HUG_MAX_FRACTION = 0.75;
const FULL_MIN_FRACTION = 0.97;

const SHORT_PROMPT = 'bubbles-short-001';
const SHORT_REPLY = 'Sure.';
const REASONING_PROMPT = 'bubbles-reasoning-002';
const REASONING_REPLY = 'Okay.';
const LONG_PROMPT = 'bubbles-long-003';
const LONG_REPLY = 'This reply runs well past three quarters of the chat column, so it wraps onto more than one line and its bubble has to span the whole row instead of hugging its content the way a one word reply does. '.repeat(2).trim();
const CODE_PROMPT = 'bubbles-code-004';
const CODE_REPLY = 'Here:\n\n```js\nconst x = 1;\n```';

let mattermost: MattermostContainer;
let aimock: AIMockContainer;
let botUserId = '';

async function sendNewChat(page: Page, prompt: string): Promise<void> {
    await new MattermostPage(page).login(mattermost.url(), username, password);
    const chatPage = new AgentChatPage(page);
    await chatPage.openNewChat(mattermost.url(), botUserId);
    await chatPage.sendNewChat(botUserId, prompt, 'button');
}

// The finished agent post that contains `text`, once the reply has ended.
async function finishedPost(page: Page, text: string): Promise<Locator> {
    const post = page.getByTestId('agent-chat-thread').getByTestId('llm-bot-post').filter({hasText: text}).last();
    await expect(post).toBeVisible({timeout: 60000});
    await expect(page.getByTestId('agent-chat-thread').getByTestId('stop-generating-button')).toHaveCount(0, {timeout: 30000});
    return post;
}

// The host bubble around the post (the post body column) and the row it sits in.
const bubbleLayout = (post: Locator) => post.evaluate((wrapper) => {
    const bubble = wrapper.closest('.post__content > .post__img + div') as HTMLElement;
    const row = bubble.parentElement as HTMLElement;
    const rowStyle = getComputedStyle(row);
    const avatar = bubble.previousElementSibling as HTMLElement;
    const available = row.clientWidth - parseFloat(rowStyle.paddingLeft) - parseFloat(rowStyle.paddingRight) -
        avatar.getBoundingClientRect().width - (parseFloat(rowStyle.columnGap) || 0);
    return {
        width: bubble.getBoundingClientRect().width,
        available,
    };
});

// The bubble's width on 30 consecutive frames: a settled bubble does not move.
const widthsOverFrames = (post: Locator) => post.evaluate((wrapper) => new Promise<number[]>((resolve) => {
    const bubble = wrapper.closest('.post__content > .post__img + div') as HTMLElement;
    const widths: number[] = [];
    const frame = () => {
        widths.push(bubble.getBoundingClientRect().width);
        if (widths.length >= 30) {
            resolve(widths);
            return;
        }
        requestAnimationFrame(frame);
    };
    requestAnimationFrame(frame);
}));

async function expectSettled(post: Locator): Promise<void> {
    const widths = await widthsOverFrames(post);
    expect(Math.max(...widths) - Math.min(...widths)).toBeLessThan(1);
}

async function expectHug(post: Locator): Promise<void> {
    await expect.poll(async () => {
        const {width, available} = await bubbleLayout(post);
        return width < HUG_MAX_FRACTION * available;
    }).toBe(true);
    await expectSettled(post);
}

async function expectFull(post: Locator): Promise<void> {
    await expect.poll(async () => {
        const {width, available} = await bubbleLayout(post);
        return width > FULL_MIN_FRACTION * available;
    }).toBe(true);
    await expectSettled(post);
}

const MATTERMOST_START_MS = 240000;
const AIMOCK_START_MS = 120000;

const test = containerTest(async (own) => {
    const mattermost = own(await withDeadline('Starting Mattermost', MATTERMOST_START_MS, () => RunAIMockContainer()));
    const aimock = own(await withDeadline('Starting aimock', AIMOCK_START_MS, () => RunAIMockSidecar(mattermost.network, {
        fixtures: [
            ...buildTextResponse({userMessage: SHORT_PROMPT, content: SHORT_REPLY}).fixtures,
            ...buildReasoningResponse({userMessage: REASONING_PROMPT, reasoning: 'The user wants a short confirmation.', content: REASONING_REPLY}).fixtures,
            ...buildTextResponse({userMessage: LONG_PROMPT, content: LONG_REPLY}).fixtures,
            ...buildTextResponse({userMessage: CODE_PROMPT, content: CODE_REPLY}).fixtures,
        ],
    })));
    return {mattermost, aimock};
});

test.describe('Agent chat bubbles', () => {
    test.describe.configure({mode: 'serial'});

    test.beforeAll(async ({containers}) => {
        ({mattermost, aimock} = containers);
        const client = await mattermost.getClient(username, password);
        const me = await client.getMe();
        botUserId = (await client.getUserByUsername(AIMOCK_BOT_NAME)).id;
        await client.createDirectChannel([me.id, botUserId]);
    });

    test('a one-word reply hugs its content', async ({page}) => {
        await sendNewChat(page, SHORT_PROMPT);
        await expectHug(await finishedPost(page, SHORT_REPLY));
    });

    test('a short reply with collapsed reasoning still hugs', async ({page}) => {
        await sendNewChat(page, REASONING_PROMPT);
        const post = await finishedPost(page, REASONING_REPLY);
        await expect(post.getByText('Thinking')).toBeVisible();
        await expectHug(post);
    });

    test('a reply wider than the hug limit spans the row', async ({page}) => {
        await sendNewChat(page, LONG_PROMPT);
        await expectFull(await finishedPost(page, 'hugging its content'));
    });

    test('a reply with a code block spans the row', async ({page}) => {
        await sendNewChat(page, CODE_PROMPT);
        await expectFull(await finishedPost(page, 'Here:'));
    });
});

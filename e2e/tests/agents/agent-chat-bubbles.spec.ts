// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Agent chat bubbles in the Agents product view: short replies hug their
// content, wide ones span the row, and a streamed reply grows without
// re-creating text that is already on screen.

import {expect, type Locator, type Page} from '@playwright/test';

import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {containerTest} from 'helpers/container-fixture';
import {withDeadline} from 'helpers/deadline';
import {buildReasoningResponse, buildTextResponse} from 'helpers/aimock-fixtures';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {AIMOCK_BOT_NAME, RunAIMockContainer} from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';

// Mirrors HUG_MAX_FRACTION in webapp/src/components/agent_chat/agent_chat_host_styles.ts.
const HUG_MAX_FRACTION = 0.75;

const SHORT_PROMPT = 'bubbles-short-001';
const SHORT_REPLY = 'Sure.';
const REASONING_PROMPT = 'bubbles-reasoning-002';
const REASONING_REPLY = 'Okay.';
const LONG_PROMPT = 'bubbles-long-003';
const LONG_REPLY = 'This reply runs well past three quarters of the chat column, so it wraps onto more than one line and its bubble has to span the whole row instead of hugging its content the way a one word reply does. '.repeat(2).trim();
const CODE_PROMPT = 'bubbles-code-004';
const CODE_REPLY = 'Here:\n\n```js\nconst x = 1;\n```';
const STREAM_PROMPT = 'bubbles-stream-005';
const STREAM_END = 'STREAMEND';
const STREAM_REPLY = [
    'Streaming text should grow steadily and never flicker. This answer is long enough to span several blocks, with **bold words**, `inline code`, *emphasis* and [a link](https://example.com/a/fairly/long/path) along the way.',
    '',
    'A second paragraph follows, long enough to wrap across a couple of lines in the chat column, so the reveal crosses line breaks as well as block boundaries.',
    '',
    '- The first list item is short.',
    '- The second list item has a little more text in it.',
    '',
    '1. An ordered list follows the bullets.',
    '2. Its second step.',
    '',
    `The last paragraph closes the answer with the marker ${STREAM_END}.`,
].join('\n');

let mattermost: MattermostContainer;
let aimock: AIMockContainer;
let botUserId = '';

async function sendNewChat(page: Page, prompt: string): Promise<void> {
    await new MattermostPage(page).login(mattermost.url(), username, password);
    await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});
    const textbox = page.getByTestId('agent-chat-new-composer').getByRole('textbox');
    await expect(textbox).toBeEnabled({timeout: 30000});
    await textbox.fill(prompt);
    await textbox.press('Enter');
}

// The finished agent post that contains `text`, once the reply has ended.
async function finishedPost(page: Page, text: string): Promise<Locator> {
    const post = page.getByTestId('agent-chat-thread').getByTestId('llm-bot-post').filter({hasText: text}).last();
    await expect(post).toBeVisible({timeout: 60000});
    await expect(page.getByTestId('agent-chat-thread').getByRole('button', {name: 'Send message'})).toBeVisible({timeout: 30000});
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
        fit: wrapper.getAttribute('data-bubble-fit'),
        flex: getComputedStyle(bubble).flex,
        width: bubble.getBoundingClientRect().width,
        available,
    };
});

// Counts fit changes over the next 30 frames: a settled bubble has none (the
// decision once flipped between hug and full on every frame).
const fitChangesOverFrames = (post: Locator) => post.evaluate((wrapper) => new Promise<number>((resolve) => {
    let changes = 0;
    let frames = 0;
    const observer = new MutationObserver(() => {
        changes += 1;
    });
    observer.observe(wrapper, {attributes: true, attributeFilter: ['data-bubble-fit']});
    const frame = () => {
        frames += 1;
        if (frames >= 30) {
            observer.disconnect();
            resolve(changes);
            return;
        }
        requestAnimationFrame(frame);
    };
    requestAnimationFrame(frame);
}));

async function expectHug(post: Locator): Promise<void> {
    await expect.poll(async () => {
        const {fit, flex} = await bubbleLayout(post);
        return {fit, flex};
    }).toEqual({fit: 'hug', flex: '0 1 auto'});
    const layout = await bubbleLayout(post);
    expect(layout.width).toBeLessThan(HUG_MAX_FRACTION * layout.available);
    expect(await fitChangesOverFrames(post)).toBe(0);
}

async function expectFull(post: Locator): Promise<void> {
    await expect.poll(async () => {
        const {fit, flex} = await bubbleLayout(post);
        return {fit, flex};
    }).toEqual({fit: 'full', flex: '1 1 auto'});
    const layout = await bubbleLayout(post);
    expect(layout.width).toBeGreaterThan(HUG_MAX_FRACTION * layout.available);
    expect(await fitChangesOverFrames(post)).toBe(0);
}

// Watches the streamed post's text from page load until the end marker is on
// screen. Revealed text sits in spans that fade in once:
// - a span in a paragraph is never removed (a re-created span replays its
//   fade, which flickers);
// - where markdown re-parsing re-creates a span anyway (a list turning loose
//   when the next list starts), text revealed well before (a second here,
//   far past the 300ms fade) shows at once instead of fading in again.
// Code blocks are excluded: the host re-highlights them on every update.
function watchSettledText() {
    const w = window as any;
    const SETTLED_MS = 1000;
    const firstSeen = new Map<string, number>();
    const removed: string[] = [];
    const refaded: string[] = [];
    let spans = 0;
    let ended = false;
    const inProse = (node: Node | null) => {
        const el = node && (node.nodeType === Node.ELEMENT_NODE ? node as Element : node.parentElement);
        return Boolean(el?.closest('[data-testid="posttext"]') && !el.closest('.post-code'));
    };
    const spansIn = (node: Node): Element[] => {
        if (node.nodeType !== Node.ELEMENT_NODE) {
            return [];
        }
        const el = node as Element;
        return el.matches('[data-fresh-text]') ? [el] : Array.from(el.querySelectorAll('[data-fresh-text]'));
    };
    const observer = new MutationObserver((records) => {
        if (ended) {
            return;
        }
        const now = performance.now();
        for (const record of records) {
            if (!inProse(record.target)) {
                continue;
            }
            record.addedNodes.forEach((node) => {
                for (const span of spansIn(node)) {
                    spans += 1;
                    const id = span.getAttribute('data-fresh-text')!;
                    const seen = firstSeen.get(id);
                    if (seen === undefined) {
                        firstSeen.set(id, now);
                    } else if (now - seen >= SETTLED_MS && span.hasAttribute('data-fade')) {
                        refaded.push(span.textContent ?? '');
                    }
                }
            });
            const inParagraph = (record.target as Element).closest?.('[data-testid="posttext"] > p');
            record.removedNodes.forEach((node) => {
                for (const span of spansIn(node)) {
                    if (inParagraph && (span.textContent ?? '').trim() !== '') {
                        removed.push(span.textContent ?? '');
                    }
                }
            });
        }
        const texts = document.querySelectorAll('[data-testid="posttext"]');
        if (texts.length > 0 && texts[texts.length - 1].textContent?.includes('STREAMEND')) {
            ended = true;
            observer.disconnect();
        }
    });
    const start = () => observer.observe(document.body, {subtree: true, childList: true});
    if (document.body) {
        start();
    } else {
        document.addEventListener('DOMContentLoaded', start);
    }
    w.__settledText = () => ({ended, spans, removed, refaded});
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

            // Small chunks, slowly, so the reveal runs over several seconds.
            {
                match: {userMessage: STREAM_PROMPT},
                response: {content: STREAM_REPLY},
                chunkSize: 6,
                streamingProfile: {ttft: 300, tps: 30, jitter: 0},
            },
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

    test('a streamed reply grows without re-creating text already on screen', async ({page}) => {
        await page.addInitScript(watchSettledText);
        await sendNewChat(page, STREAM_PROMPT);
        const post = await finishedPost(page, STREAM_END);

        const watched = await page.evaluate(() => (window as any).__settledText());
        expect(watched.ended).toBe(true);

        // The reply was revealed in fading spans (smoothing is on here); paragraph
        // text was never re-created and settled text never faded in again.
        expect(watched.spans).toBeGreaterThan(0);
        expect(watched.removed).toEqual([]);
        expect(watched.refaded).toEqual([]);

        // The whole answer is shown, with no reveal marker characters in it.
        await expect(post.getByTestId('posttext')).toContainText(`with the marker ${STREAM_END}.`);
        const text = await post.getByTestId('posttext').textContent();
        expect(text).not.toMatch(/[-]/);
    });
});

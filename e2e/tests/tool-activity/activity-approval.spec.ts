import {test, expect, type Locator} from '@playwright/test';

import {AIPlugin} from 'helpers/ai-plugin';
import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {
    buildMultiTurnToolSequence,
    buildTitleFixture,
    EMBEDDED_GET_CHANNEL_INFO_TOOL,
    mergeFixtureFiles,
} from 'helpers/aimock-fixtures';
import {
    expandToolActivity,
    expectToolActivityCollapsed,
    expectToolActivityCurrent,
    expectToolActivitySummary,
    TOOL_STATUS_SELECTOR,
} from 'helpers/llmbot-post';
import {MattermostPage} from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import {RunToolConfigAIMockContainer, setupRegularTestUser} from 'helpers/tool-config-container';

/**
 * Where a round the requester owes a decision on renders relative to the
 * collapsed activity area, and what the activity summary says once the
 * decision has been made either way.
 */

const username = 'regularuser';
const password = 'regularuser';

const EMBEDDED_READ_CHANNEL_TOOL = 'mattermost__read_channel';
const getChannelInfoLabel = 'Get Channel Info';
const readChannelLabel = 'Read Channel';

const acceptPrompt = 'tool activity approval after prelude';
const acceptFinal = 'APPROVAL_ANSWER: the lookup is complete.';

const rejectPrompt = 'tool activity rejection after prelude';

// Only reached if the plugin resumes after a rejection, which it does not in a
// DM; the fixture keeps aimock's strict matching satisfied either way.
const rejectFinal = 'REJECTION_ANSWER: I skipped the lookup.';

/**
 * True when the Accept button is a sibling that follows the activity area
 * rather than something nested inside it.
 */
async function approvalRendersBelowActivity(post: Locator): Promise<boolean> {
    return post.evaluate((el) => {
        const activity = el.querySelector('[data-testid="llm-bot-tool-activity"]');
        const accept = Array.from(el.querySelectorAll('button')).find(
            (button) => button.textContent?.trim() === 'Accept',
        );
        if (!activity || !accept) {
            return false;
        }

        const relation = activity.compareDocumentPosition(accept);
        const follows = (relation & Node.DOCUMENT_POSITION_FOLLOWING) !== 0;
        const nested = (relation & Node.DOCUMENT_POSITION_CONTAINED_BY) !== 0;
        return follows && !nested;
    });
}

test.describe('Tool Activity Approval Placement (Aimock)', () => {
    test.describe.configure({mode: 'serial'});

    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;

    test.beforeAll(async () => {
        test.setTimeout(300000);
        mattermost = await RunToolConfigAIMockContainer({
            toolConfigs: [
                {name: 'read_channel', policy: 'auto_run_in_dm', enabled: true},
                {name: 'get_channel_info', policy: 'ask', enabled: true},
            ],
        });
        await setupRegularTestUser(mattermost);

        const userClient = await mattermost.getClient(username, password);
        const teams = await userClient.getMyTeams();
        const channels = await userClient.getMyChannels(teams[0].id);
        const townSquare = channels.find((channel: {name: string}) => channel.name === 'town-square');
        if (!townSquare) {
            throw new Error('town-square channel not found');
        }

        const preludeThenAsk = (options: {
            prompt: string;
            readCallId: string;
            infoCallId: string;
            finalText: string;
        }) => buildMultiTurnToolSequence({
            userPromptMarker: options.prompt,
            steps: [
                {
                    toolCallId: options.readCallId,
                    toolName: EMBEDDED_READ_CHANNEL_TOOL,
                    args: {channel_id: townSquare.id, limit: 5},
                },
                {
                    matchAfterToolCallId: options.readCallId,
                    toolCallId: options.infoCallId,
                    toolName: EMBEDDED_GET_CHANNEL_INFO_TOOL,
                    args: {channel_name: 'Town Square'},
                },
                {matchAfterToolCallId: options.infoCallId, text: options.finalText},
            ],
        });

        // The title fixture matches by substring and every title request embeds
        // the original prompt, so it has to be matched first.
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: mergeFixtureFiles(
                {fixtures: [buildTitleFixture('Tool activity approval')]},
                preludeThenAsk({
                    prompt: acceptPrompt,
                    readCallId: 'call_ta_accept_read',
                    infoCallId: 'call_ta_accept_info',
                    finalText: acceptFinal,
                }),
                preludeThenAsk({
                    prompt: rejectPrompt,
                    readCallId: 'call_ta_reject_read',
                    infoCallId: 'call_ta_reject_info',
                    finalText: rejectFinal,
                }),
            ),
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        await mattermost?.stop();
    });

    test('renders the approval card below the collapsed activity area and folds it back in on accept', async ({page}) => {
        test.setTimeout(180000);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.resetState();
        await aiPlugin.sendMessage(acceptPrompt);

        const rhs = page.getByTestId('mattermost-ai-rhs');
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();

        const acceptButton = rhs.getByRole('button', {name: /^accept$/i});
        await expect(acceptButton).toBeVisible({timeout: 120000});

        // The auto-run prelude is folded away while the round that needs a
        // decision renders in full, below the collapsed row.
        await expectToolActivityCollapsed(botPost);
        await expectToolActivityCurrent(botPost, readChannelLabel);
        await expect(botPost.getByText(getChannelInfoLabel, {exact: true})).toBeVisible();
        expect(await approvalRendersBelowActivity(botPost)).toBe(true);

        await acceptButton.click();

        await expect(botPost.getByText(acceptFinal)).toBeVisible({timeout: 120000});
        await expect(page.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});
        await expect(rhs.getByRole('button', {name: /^accept$/i})).not.toBeVisible();
        await expect(rhs.getByRole('button', {name: /^reject$/i})).not.toBeVisible();

        // With no decision outstanding, both tools fold into one summary.
        await expectToolActivityCollapsed(botPost);
        await expectToolActivitySummary(botPost, 2, 'success');
        await expect(botPost.getByText(getChannelInfoLabel, {exact: true})).toHaveCount(0);

        const activityRounds = await expandToolActivity(botPost);
        await expect(activityRounds.getByText(readChannelLabel, {exact: true})).toBeVisible({timeout: 30000});
        await expect(activityRounds.getByText(getChannelInfoLabel, {exact: true})).toBeVisible();
    });

    test('summarizes a rejected tool as rejected', async ({page}) => {
        test.setTimeout(180000);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.resetState();
        await aiPlugin.sendMessage(rejectPrompt);

        const rhs = page.getByTestId('mattermost-ai-rhs');
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();

        const rejectButton = rhs.getByRole('button', {name: /^reject$/i});
        await expect(rejectButton).toBeVisible({timeout: 120000});
        await rejectButton.click();

        // Rejecting the only pending call ends the response: the plugin does
        // not resume the model, so the post settles on the activity summary.
        await expect(rejectButton).not.toBeVisible({timeout: 60000});
        await expect(rhs.getByRole('button', {name: /^accept$/i})).not.toBeVisible();
        await expect(page.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});

        // Two tools ran; the summary reports the worst outcome among them.
        await expectToolActivitySummary(botPost, 2, 'rejected');

        // The rejected call is still in the stack, carrying the glyph the
        // summary borrowed from it.
        const activityRounds = await expandToolActivity(botPost);
        const rejectedCard = activityRounds.
            locator('[class*="ToolCallCard"]').
            filter({hasText: getChannelInfoLabel});
        await expect(rejectedCard).toBeVisible({timeout: 30000});
        await expect(rejectedCard.locator(TOOL_STATUS_SELECTOR)).toHaveAttribute('data-status', 'rejected');
    });
});

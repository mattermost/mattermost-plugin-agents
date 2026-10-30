// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Page} from '@playwright/test';

// On servers with registerChannelViewComponent the plugin shows an agent's DM in the Channels app as
// the agent chat instead of the post list and composer. These helpers let specs tell which view a
// host gives, and let specs that drive the stock DM switch it back.

const PLUGIN_STATE_KEY = 'plugins-mattermost-ai';

// The action the agent view's "View as messages" and the header's "View as agent chat" dispatch (webapp/src/agent_dm_view.ts).
const RAW_VIEW_ACTION = 'AI_AGENT_DM_RAW_VIEW_SET';

const DECISION_TIMEOUT_MS = 30000;

/** Waits until the plugin's agents are loaded and `channelId` is the open channel, so the host has picked the DM's view. */
async function waitForViewDecision(page: Page, channelId: string): Promise<void> {
    await expect.poll(() => page.evaluate(({key, id}) => {
        const state = (window as any).store?.getState();
        return Boolean(state?.[key]?.bots) && state?.entities?.channels?.currentChannelId === id;
    }, {key: PLUGIN_STATE_KEY, id: channelId}), {timeout: DECISION_TIMEOUT_MS}).toBe(true);
}

/**
 * Whether the host shows the open agent DM `channelId` as the agent chat. False on hosts without
 * registerChannelViewComponent, where the DM keeps its post list.
 */
export async function hostShowsAgentDMView(page: Page, channelId: string): Promise<boolean> {
    await waitForViewDecision(page, channelId);
    const agentView = page.getByTestId('agent-dm-channel-view');

    // The channel's own composer (the agent view's new-chat composer is a host editor too, outside it).
    await expect(agentView.or(page.getByTestId('post-create')).first()).toBeVisible({timeout: DECISION_TIMEOUT_MS});
    return agentView.isVisible();
}

/**
 * Shows the agent DM `channelId` (default: the DM being opened, once it is the current channel) as
 * plain messages for this page session, as the agent view's "View as messages" does. A no-op on
 * hosts that keep agent DMs as plain messages anyway. Call it after the page has loaded (it waits
 * for the plugin's store).
 */
export async function showAgentDMAsMessages(page: Page, channelId?: string): Promise<void> {
    let id = channelId ?? '';
    await expect.poll(async () => {
        const current = await page.evaluate((key) => {
            const state = (window as any).store?.getState();
            if (!state?.[key]) {
                return null;
            }
            const currentId = state.entities?.channels?.currentChannelId ?? '';
            return state.entities?.channels?.channels?.[currentId]?.type === 'D' ? currentId : '';
        }, PLUGIN_STATE_KEY);
        if (current !== null && !id) {
            id = current;
        }
        return current !== null && Boolean(id);
    }, {timeout: DECISION_TIMEOUT_MS}).toBe(true);
    await page.evaluate(({type, channel}) => {
        (window as any).store.dispatch({type, channelId: channel, raw: true});
    }, {type: RAW_VIEW_ACTION, channel: id});
}

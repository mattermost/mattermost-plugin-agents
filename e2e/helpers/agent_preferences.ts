import type {Client4} from '@mattermost/client';
import {ClientError} from '@mattermost/client';
import {expect} from '@playwright/test';

import MattermostContainer from './mmcontainer';

// Must match webapp/src/bots.tsx preference contract.
export const SELECTED_AGENT_PREFERENCE_CATEGORY = 'agents';
export const SELECTED_AGENT_PREFERENCE_NAME = 'selected_agent';

export async function selectedAgentPreference(client: Client4, userID: string): Promise<string> {
    const preferences = await client.getUserPreferences(userID);
    return preferences.find((preference) => (
        preference.category === SELECTED_AGENT_PREFERENCE_CATEGORY &&
        preference.name === SELECTED_AGENT_PREFERENCE_NAME
    ))?.value ?? '';
}

export async function expectSelectedAgentPreference(
    client: Client4,
    userID: string,
    agentID: string,
): Promise<void> {
    await expect.poll(
        () => selectedAgentPreference(client, userID),
        {
            message: `selected agent preference did not persist as ${agentID}`,
            timeout: 30000,
            intervals: [250, 500, 1000],
        },
    ).toBe(agentID);
}

function isMissingPreferenceError(err: unknown): boolean {
    return err instanceof ClientError && err.status_code === 404;
}

export async function resetSelectedAgentPreference(
    mattermost: MattermostContainer,
    username: string,
    password: string,
): Promise<void> {
    const client = await mattermost.getClient(username, password);
    const user = await client.getMe();
    try {
        await client.deletePreferences(user.id, [{
            user_id: user.id,
            category: SELECTED_AGENT_PREFERENCE_CATEGORY,
            name: SELECTED_AGENT_PREFERENCE_NAME,
        }]);
    } catch (err) {
        if (isMissingPreferenceError(err)) {
            return;
        }
        throw err;
    }
}

import MattermostContainer from './mmcontainer';

// Must match webapp/src/bots.tsx preference contract.
export const SELECTED_AGENT_PREFERENCE_CATEGORY = 'agents';
export const SELECTED_AGENT_PREFERENCE_NAME = 'selected_agent';

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
    } catch {
        // Preference may not exist.
    }
}

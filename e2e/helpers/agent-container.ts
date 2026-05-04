import { RunSystemConsoleContainer } from './system-console-container';
import MattermostContainer from './mmcontainer';

export const agentAdminUsername = 'sysadmin';
export const agentAdminPassword = 'Sys@dmin-sample1';
export const agentRegularUsername = 'regularuser';
export const agentRegularPassword = 'regularuser';
export const agentUnprivilegedUsername = 'unprivileged';
export const agentUnprivilegedPassword = 'unprivileged';

export const mockServiceId = 'mock-service';
export const secondServiceId = 'second-service';

/**
 * Run a Mattermost container configured for agent E2E tests.
 * - Two mock LLM services (for service switching tests)
 * - MCP enabled with embedded server (for tool selection tests)
 * - Three users: admin (sysadmin), regularuser, unprivileged (self-service agent perms come from RunSystemConsoleContainer)
 */
export async function RunAgentContainer(): Promise<MattermostContainer> {
    const mattermost = await RunSystemConsoleContainer({
        services: [
            {
                id: mockServiceId,
                name: 'Mock Service',
                type: 'openaicompatible',
                apiKey: 'mock',
                apiURL: 'http://openai:8080',
                defaultModel: 'gpt-mock',
                useResponsesAPI: false,
            },
            {
                id: secondServiceId,
                name: 'Second Service',
                type: 'openaicompatible',
                apiKey: 'mock2',
                apiURL: 'http://openai:8080/second',
                defaultModel: 'gpt-mock-2',
                useResponsesAPI: false,
            },
        ],
        bots: [
            {
                id: 'default-bot-id',
                name: 'mock',
                displayName: 'Mock Bot',
                serviceID: mockServiceId,
                customInstructions: '',
            },
        ],
        defaultBotName: 'mock',
        mcp: {
            enabled: true,
            enablePluginServer: true,
            embeddedServer: { enabled: true },
            idleTimeoutMinutes: 30,
            servers: [],
        },
    });

    // Create additional test users
    await mattermost.createUser(
        'regularuser@sample.com', agentRegularUsername, agentRegularPassword
    );
    await mattermost.addUserToTeam(agentRegularUsername, 'test');

    await mattermost.createUser(
        'unprivileged@sample.com', agentUnprivilegedUsername, agentUnprivilegedPassword
    );
    await mattermost.addUserToTeam(agentUnprivilegedUsername, 'test');

    // Set user preferences to skip tours/onboarding
    for (const [username, password] of [
        [agentRegularUsername, agentRegularPassword],
        [agentUnprivilegedUsername, agentUnprivilegedPassword],
    ] as const) {
        const client = await mattermost.getClient(username, password);
        const user = await client.getMe();
        await client.savePreferences(user.id, [
            { user_id: user.id, category: 'tutorial_step', name: user.id, value: '999' },
            { user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false' },
            { user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false' },
            {
                user_id: user.id,
                category: 'drafts',
                name: 'drafts_tour_tip_showed',
                value: JSON.stringify({ drafts_tour_tip_showed: true }),
            },
            { user_id: user.id, category: 'crt_thread_pane_step', name: user.id, value: '999' },
        ]);
    }

    return mattermost;
}

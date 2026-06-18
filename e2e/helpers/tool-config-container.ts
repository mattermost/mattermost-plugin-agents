import RunSystemConsoleContainer from './system-console-container';
import MattermostContainer from './mmcontainer';
import {AIMOCK_COMPATIBLE_SERVICE} from './aimock-fixtures';

export type ToolPolicyConfig = {
    name: string;
    policy: 'ask' | 'auto_run_in_dm' | 'auto_run_everywhere';
    enabled: boolean;
};

export type ToolConfigAIMockOptions = {
    toolConfigs?: ToolPolicyConfig[];
    customInstructions?: string;
    enableVectorIndex?: boolean;
    defaultBotName?: string;
    botId?: string;
    botDisplayName?: string;
};

export const MULTIPLAYER_ASK_TOOL_CONFIGS: ToolPolicyConfig[] = [
    {name: 'get_channel_info', policy: 'ask', enabled: true},
    {name: 'create_post', policy: 'ask', enabled: true},
];

/** Creates regularuser with standard onboarding prefs for tool-config aimock specs. */
export async function setupRegularTestUser(mattermost: MattermostContainer): Promise<void> {
    await mattermost.createUser('regularuser@sample.com', 'regularuser', 'regularuser');
    await mattermost.addUserToTeam('regularuser', 'test');

    const userClient = await mattermost.getClient('regularuser', 'regularuser');
    const user = await userClient.getMe();
    await userClient.savePreferences(user.id, [
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

/**
 * Plugin config for tool-config E2E tests.
 *
 * Uses Smocker mock as LLM + enables MCP with the embedded server.
 * The embedded server provides real Mattermost tools for testing tool configs.
 */
export async function RunToolConfigContainer(): Promise<MattermostContainer> {
    return RunSystemConsoleContainer({
        services: [
            {
                id: 'mock-service',
                name: 'Mock Service',
                type: 'openaicompatible',
                apiKey: 'mock',
                apiURL: 'http://openai:8080',
                defaultModel: 'gpt-mock',
                useResponsesAPI: false,
            },
        ],
        bots: [
            {
                id: 'tool-test-bot',
                name: 'toolbot',
                displayName: 'Tool Test Bot',
                serviceID: 'mock-service',
                customInstructions: '',
                enabledNativeTools: [],
            },
        ],
        mcp: {
            enabled: true,
            enablePluginServer: true,
            embeddedServer: { enabled: true },
            idleTimeoutMinutes: 30,
            servers: [],
        },
    });
}

/**
 * Container with MCP embedded server and explicit tool_configs
 * preset for testing policy changes. Uses same base config.
 */
export async function RunToolConfigContainerWithPolicies(): Promise<MattermostContainer> {
    return RunSystemConsoleContainer({
        enableChannelMentionToolCalling: true,
        services: [
            {
                id: 'mock-service',
                name: 'Mock Service',
                type: 'openaicompatible',
                apiKey: 'mock',
                apiURL: 'http://openai:8080',
                defaultModel: 'gpt-mock',
                // Smocker only mocks /v1/chat/completions; Responses API would miss mocks and fail streaming.
                useResponsesAPI: false,
            },
        ],
        bots: [
            {
                id: 'tool-test-bot',
                name: 'toolbot',
                displayName: 'Tool Test Bot',
                serviceID: 'mock-service',
                customInstructions: '',
                disableTools: false,
                // This suite asserts legacy per-tool approval behavior using
                // direct mocked business-tool calls (no search/load prelude).
                // Keep dynamic loading off here so the strict JIT registry
                // does not hide the tools under test.
                mcpDynamicToolLoading: false,
                enabledNativeTools: [],
            },
        ],
        mcp: {
            enabled: true,
            enablePluginServer: true,
            embeddedServer: { enabled: true },
            idleTimeoutMinutes: 30,
            servers: [],
        },
    });
}

/**
 * Container for dynamic MCP tool-loading E2E tests.
 * Enables mcpDynamicToolLoading so only search_tools/load_tool are visible
 * initially; business tools must be discovered and loaded first.
 */
export async function RunToolConfigContainerWithDynamicPolicies(): Promise<MattermostContainer> {
    return RunSystemConsoleContainer({
        enableChannelMentionToolCalling: true,
        services: [
            {
                id: 'mock-service',
                name: 'Mock Service',
                type: 'openaicompatible',
                apiKey: 'mock',
                apiURL: 'http://openai:8080',
                defaultModel: 'gpt-mock',
                useResponsesAPI: false,
            },
        ],
        bots: [
            {
                id: 'tool-test-bot',
                name: 'toolbot',
                displayName: 'Tool Test Bot',
                serviceID: 'mock-service',
                customInstructions: '',
                disableTools: false,
                mcpDynamicToolLoading: true,
                enabledNativeTools: [],
            },
        ],
        mcp: {
            enabled: true,
            enablePluginServer: true,
            embeddedServer: {
                enabled: true,
                tool_configs: [
                    {name: 'get_channel_info', policy: 'ask', enabled: true},
                ],
            },
            idleTimeoutMinutes: 30,
            servers: [],
        },
    });
}

/**
 * Aimock-backed tool-config container for migrated real-API policy suites.
 * Smocker-backed helpers above remain unchanged for mock-api shards.
 * Callers that need regularuser should invoke setupRegularTestUser separately.
 */
export async function RunToolConfigAIMockContainer(
    options: ToolConfigAIMockOptions | ToolPolicyConfig[] = {},
): Promise<MattermostContainer> {
    const resolved: ToolConfigAIMockOptions = Array.isArray(options)
        ? {toolConfigs: options}
        : options;

    const {
        toolConfigs = [],
        customInstructions = '',
        enableVectorIndex = false,
        defaultBotName,
        botId = 'aimock-toolbot',
        botDisplayName = 'Aimock Tool Bot',
    } = resolved;

    return RunSystemConsoleContainer({
        enableChannelMentionToolCalling: true,
        enableVectorIndex,
        defaultBotName,
        services: [{...AIMOCK_COMPATIBLE_SERVICE}],
        bots: [
            {
                id: botId,
                name: 'toolbot',
                displayName: botDisplayName,
                serviceID: AIMOCK_COMPATIBLE_SERVICE.id,
                customInstructions,
                disableTools: false,
                mcpDynamicToolLoading: false,
                enabledNativeTools: [],
            },
        ],
        mcp: {
            enabled: true,
            enablePluginServer: true,
            embeddedServer: {
                enabled: true,
                tool_configs: toolConfigs,
            },
            idleTimeoutMinutes: 30,
            servers: [],
        },
    });
}

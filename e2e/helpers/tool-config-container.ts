import RunSystemConsoleContainer from './system-console-container';
import MattermostContainer from './mmcontainer';

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

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

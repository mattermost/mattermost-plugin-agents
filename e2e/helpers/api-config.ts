/**
 * API Configuration for E2E Tests
 *
 * Provides factory functions for creating LLM service and bot configurations
 * based on environment variables. Tests can customize these base configs.
 *
 * Note: These types mirror the webapp types but are defined here to avoid
 * import issues with webpack-specific assets in the webapp files.
 */

// Default models for each provider. Update these when bumping model versions.
export const DEFAULT_ANTHROPIC_MODEL = 'claude-sonnet-4-6';
export const DEFAULT_OPENAI_MODEL = 'gpt-5.2';

/**
 * LLM Service Configuration
 * Mirrors webapp/src/components/system_console/service.tsx LLMService type
 */
export interface LLMService {
    id: string;
    name: string;
    type: string;
    apiURL: string;
    apiKey: string;
    orgId: string;
    defaultModel: string;
    tokenLimit: number;
    streamingTimeoutSeconds: number;
    sendUserId: boolean;
    outputTokenLimit: number;
    useResponsesAPI: boolean;
}

/**
 * Channel Access Level
 * Mirrors webapp/src/components/system_console/bot.tsx ChannelAccessLevel enum
 */
export enum ChannelAccessLevel {
    All = 0,
    Allow,
    Block,
    None,
}

/**
 * User Access Level
 * Mirrors webapp/src/components/system_console/bot.tsx UserAccessLevel enum
 */
export enum UserAccessLevel {
    All = 0,
    Allow,
    Block,
    None,
}

/**
 * LLM Bot Configuration
 * Mirrors webapp/src/components/system_console/bot.tsx LLMBotConfig type
 */
export interface LLMBotConfig {
    id: string;
    name: string;
    displayName: string;
    serviceID: string;
    customInstructions: string;
    enableVision: boolean;
    disableTools: boolean;
    channelAccessLevel: ChannelAccessLevel;
    channelIDs: string[];
    userAccessLevel: UserAccessLevel;
    userIDs: string[];
    teamIDs: string[];
    enabledNativeTools?: string[];
    reasoningEnabled?: boolean;
    reasoningEffort?: string;
    thinkingBudget?: number;
}

export interface APITestConfig {
    hasAnthropicKey: boolean;
    hasOpenAIKey: boolean;
    shouldRunTests: boolean;
    requestedProvider: 'anthropic' | 'openai' | null;
}

function getRequestedProvider(): 'anthropic' | 'openai' | null {
    const rawProvider = process.env.E2E_PROVIDER?.trim().toLowerCase();

    if (!rawProvider) {
        return null;
    }

    if (rawProvider !== 'anthropic' && rawProvider !== 'openai') {
        throw new Error(`Unsupported E2E_PROVIDER value: ${rawProvider}`);
    }

    return rawProvider;
}

/**
 * Get API test configuration from environment
 */
export function getAPIConfig(): APITestConfig {
    const anthropicKey = process.env.ANTHROPIC_API_KEY;
    const openaiKey = process.env.OPENAI_API_KEY;
    const requestedProvider = getRequestedProvider();
    const hasAnthropicKey = !!anthropicKey && anthropicKey.length > 0;
    const hasOpenAIKey = !!openaiKey && openaiKey.length > 0;

    let shouldRunTests = hasAnthropicKey || hasOpenAIKey;
    if (requestedProvider === 'anthropic') {
        shouldRunTests = hasAnthropicKey;
    } else if (requestedProvider === 'openai') {
        shouldRunTests = hasOpenAIKey;
    }

    return {
        hasAnthropicKey,
        hasOpenAIKey,
        shouldRunTests,
        requestedProvider,
    };
}

/**
 * Get skip message for tests when no API keys are present
 */
export function getSkipMessage(): string | null {
    const config = getAPIConfig();
    if (!config.shouldRunTests) {
        if (config.requestedProvider === 'anthropic') {
            return 'Skipping real API tests: E2E_PROVIDER=anthropic was requested but ANTHROPIC_API_KEY is not configured.';
        }

        if (config.requestedProvider === 'openai') {
            return 'Skipping real API tests: E2E_PROVIDER=openai was requested but OPENAI_API_KEY is not configured.';
        }

        return 'Skipping llmbot-post-component tests: No ANTHROPIC_API_KEY or OPENAI_API_KEY found in environment. Set one to run these tests with real APIs.';
    }
    return null;
}

/**
 * Log API configuration for debugging
 */
export function logAPIConfig(): void {
    const config = getAPIConfig();
    if (!config.shouldRunTests) {
        console.log('⚠️  LLMBot tests SKIPPED - No API keys configured');
        console.log('   Set ANTHROPIC_API_KEY or OPENAI_API_KEY to enable');
        return;
    }

    const providers = getAvailableProviders();
    const providerScope = config.requestedProvider ? ` (${config.requestedProvider} only)` : '';
    console.log(`🔴 LLMBot tests using REAL APIs${providerScope}:`);
    for (const p of providers) {
        console.log(`   - ${p.name}: ${p.service.defaultModel}`);
    }
    console.log('   ⚠️  This will incur API costs (~$0.05 per run)');
}

/**
 * Partial type for service customization
 */
export type ServiceConfigOverrides = Partial<Omit<LLMService, 'id'>>;

/**
 * Partial type for bot customization
 */
export type BotConfigOverrides = Partial<Omit<LLMBotConfig, 'id' | 'serviceID'>>;

/**
 * Create a default Anthropic service configuration
 */
export function createAnthropicService(overrides: ServiceConfigOverrides = {}): LLMService {
    const apiKey = process.env.ANTHROPIC_API_KEY;
    if (!apiKey) {
        throw new Error('ANTHROPIC_API_KEY not found in environment');
    }

    return {
        id: 'anthropic-service',
        name: 'Anthropic Service',
        type: 'anthropic',
        apiKey,
        apiURL: 'https://api.anthropic.com',
        orgId: '',
        defaultModel: process.env.ANTHROPIC_MODEL || DEFAULT_ANTHROPIC_MODEL,
        tokenLimit: 16384,
        outputTokenLimit: 16384,
        streamingTimeoutSeconds: 0,
        sendUserId: false,
        useResponsesAPI: false,
        ...overrides,
    };
}

/**
 * Create a default OpenAI service configuration
 */
export function createOpenAIService(overrides: ServiceConfigOverrides = {}): LLMService {
    const apiKey = process.env.OPENAI_API_KEY;
    if (!apiKey) {
        throw new Error('OPENAI_API_KEY not found in environment');
    }

    return {
        id: 'openai-service',
        name: 'OpenAI Service',
        type: 'openaicompatible',
        apiKey,
        apiURL: 'https://api.openai.com/v1',
        orgId: '',
        defaultModel: process.env.OPENAI_MODEL || DEFAULT_OPENAI_MODEL,
        tokenLimit: 16384,
        outputTokenLimit: 16384,
        streamingTimeoutSeconds: 500,
        sendUserId: false,
        useResponsesAPI: true,
        ...overrides,
    };
}

/**
 * Create a default bot configuration
 */
export function createBotConfig(
    service: LLMService,
    overrides: BotConfigOverrides = {}
): LLMBotConfig {
    const isAnthropic = service.type === 'anthropic';
    const botName = isAnthropic ? 'claude' : 'mockbot';
    const displayName = isAnthropic ? 'Claude Bot' : 'OpenAI Bot';

    return {
        id: `${service.id}-bot-id`,
        name: botName,
        displayName,
        serviceID: service.id,
        customInstructions: '',
        enableVision: false,
        disableTools: false,
        channelAccessLevel: ChannelAccessLevel.All,
        channelIDs: [],
        userAccessLevel: UserAccessLevel.All,
        userIDs: [],
        teamIDs: [],
        enabledNativeTools: ['web_search'],
        reasoningEnabled: true,
        ...(isAnthropic && {
            thinkingBudget: 1024,
        }),
        ...(service.useResponsesAPI && {
            reasoningEffort: 'high',
        }),
        ...overrides,
    };
}

/**
 * Provider configuration bundle
 */
export interface ProviderBundle {
    name: string;
    service: LLMService;
    bot: LLMBotConfig;
}

/**
 * Get all available provider bundles from environment
 */
export function getAvailableProviders(): ProviderBundle[] {
    const config = getAPIConfig();
    const providers: ProviderBundle[] = [];

    if (config.hasAnthropicKey && (config.requestedProvider === null || config.requestedProvider === 'anthropic')) {
        const service = createAnthropicService();
        const bot = createBotConfig(service);
        providers.push({
            name: 'Anthropic',
            service,
            bot,
        });
    }

    if (config.hasOpenAIKey && (config.requestedProvider === null || config.requestedProvider === 'openai')) {
        const service = createOpenAIService();
        const bot = createBotConfig(service);
        providers.push({
            name: 'OpenAI',
            service,
            bot,
        });
    }

    return providers;
}

/**
 * Create a custom provider bundle with specific overrides
 */
export function createCustomProvider(
    providerType: 'anthropic' | 'openai',
    serviceOverrides: ServiceConfigOverrides = {},
    botOverrides: BotConfigOverrides = {}
): ProviderBundle {
    const service = providerType === 'anthropic'
        ? createAnthropicService(serviceOverrides)
        : createOpenAIService(serviceOverrides);

    const bot = createBotConfig(service, botOverrides);

    return {
        name: providerType === 'anthropic' ? 'Anthropic' : 'OpenAI',
        service,
        bot,
    };
}

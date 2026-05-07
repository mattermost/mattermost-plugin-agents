// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

const fs = require('fs');
const path = require('path');

const specPath = path.join(__dirname, '..', 'api', 'openapi.yaml');
const spec = fs.readFileSync(specPath, 'utf8');

const expected = [
    ['GET', '/plugins/mattermost-ai/bridge/v1/agents'],
    ['GET', '/plugins/mattermost-ai/bridge/v1/services'],
    ['POST', '/plugins/mattermost-ai/bridge/v1/completion/agent/{agent}'],
    ['POST', '/plugins/mattermost-ai/bridge/v1/completion/agent/{agent}/nostream'],
    ['POST', '/plugins/mattermost-ai/bridge/v1/completion/service/{service}'],
    ['POST', '/plugins/mattermost-ai/bridge/v1/completion/service/{service}/nostream'],
    ['GET', '/plugins/mattermost-ai/mcp-server/.well-known/oauth-protected-resource'],
    ['GET', '/plugins/mattermost-ai/mcp-server/mcp'],
    ['POST', '/plugins/mattermost-ai/mcp-server/mcp'],
    ['DELETE', '/plugins/mattermost-ai/mcp-server/mcp'],
    ['GET', '/plugins/mattermost-ai/conversations/{conversationid}'],
    ['GET', '/plugins/mattermost-ai/oauth/callback'],
    ['GET', '/plugins/mattermost-ai/ai_threads'],
    ['GET', '/plugins/mattermost-ai/ai_bots'],
    ['GET', '/plugins/mattermost-ai/mcp/tools'],
    ['GET', '/plugins/mattermost-ai/mcp/oauth/{serverName}/start'],
    ['GET', '/plugins/mattermost-ai/mcp/user-preferences'],
    ['PUT', '/plugins/mattermost-ai/mcp/user-preferences'],
    ['DELETE', '/plugins/mattermost-ai/mcp/oauth/{serverName}'],
    ['POST', '/plugins/mattermost-ai/agents'],
    ['GET', '/plugins/mattermost-ai/agents'],
    ['POST', '/plugins/mattermost-ai/agents/models/fetch'],
    ['GET', '/plugins/mattermost-ai/agents/{agentid}'],
    ['PUT', '/plugins/mattermost-ai/agents/{agentid}'],
    ['DELETE', '/plugins/mattermost-ai/agents/{agentid}'],
    ['POST', '/plugins/mattermost-ai/agents/{agentid}/avatar'],
    ['GET', '/plugins/mattermost-ai/services'],
    ['POST', '/plugins/mattermost-ai/search/raw'],
    ['POST', '/plugins/mattermost-ai/custom-prompts'],
    ['GET', '/plugins/mattermost-ai/custom-prompts'],
    ['PUT', '/plugins/mattermost-ai/custom-prompts/{id}'],
    ['DELETE', '/plugins/mattermost-ai/custom-prompts/{id}'],
    ['GET', '/plugins/mattermost-ai/custom-prompts/pins'],
    ['PUT', '/plugins/mattermost-ai/custom-prompts/pins'],
    ['POST', '/plugins/mattermost-ai/custom-prompts/{id}/render'],
    ['POST', '/plugins/mattermost-ai/post/{postid}/react'],
    ['POST', '/plugins/mattermost-ai/post/{postid}/analyze'],
    ['POST', '/plugins/mattermost-ai/post/{postid}/transcribe/file/{fileid}'],
    ['POST', '/plugins/mattermost-ai/post/{postid}/summarize_transcription'],
    ['POST', '/plugins/mattermost-ai/post/{postid}/stop'],
    ['POST', '/plugins/mattermost-ai/post/{postid}/regenerate'],
    ['POST', '/plugins/mattermost-ai/post/{postid}/tool_call'],
    ['POST', '/plugins/mattermost-ai/post/{postid}/tool_result'],
    ['POST', '/plugins/mattermost-ai/post/{postid}/postback_summary'],
    ['POST', '/plugins/mattermost-ai/channel/{channelid}/analyze'],
    ['POST', '/plugins/mattermost-ai/channel/{channelid}/interval'],
    ['POST', '/plugins/mattermost-ai/admin/reindex'],
    ['GET', '/plugins/mattermost-ai/admin/reindex/status'],
    ['POST', '/plugins/mattermost-ai/admin/reindex/cancel'],
    ['POST', '/plugins/mattermost-ai/admin/reindex/catchup'],
    ['GET', '/plugins/mattermost-ai/admin/reindex/health-check'],
    ['GET', '/plugins/mattermost-ai/admin/mcp/tools'],
    ['GET', '/plugins/mattermost-ai/admin/mcp/vetted-tool-seed'],
    ['POST', '/plugins/mattermost-ai/admin/mcp/tools/cache/clear'],
    ['POST', '/plugins/mattermost-ai/admin/models/fetch'],
    ['GET', '/plugins/mattermost-ai/admin/config'],
    ['PUT', '/plugins/mattermost-ai/admin/config'],
    ['POST', '/plugins/mattermost-ai/search'],
    ['POST', '/plugins/mattermost-ai/search/run'],
];

const PLUGIN_ROUTE_PREFIX = '/plugins/mattermost-ai';

/**
 * HTTP verbs supported as OpenAPI operation keys (lowercase).
 */
const HTTP_METHODS = new Set(['get', 'post', 'put', 'delete', 'patch', 'options', 'head', 'trace']);

/**
 * Non-operation keys allowed directly under a path item (lower-case comparison).
 */
const ALLOWED_PATH_ITEM_KEYS = new Set([
    ...HTTP_METHODS,
    'parameters',
    'servers',
    'summary',
    'description',
    'externaldocs',
    '$ref',
    'callbacks',
]);

const documented = new Set();
let inPathsSection = false;
let currentPath = '';
for (const line of spec.split(/\r?\n/)) {
    if (line === 'paths:') {
        inPathsSection = true;
        continue;
    }

    // Everything after `components:` uses similar indentation; stop scanning paths here.
    if (line === 'components:') {
        inPathsSection = false;
        currentPath = '';
        continue;
    }

    if (!inPathsSection) {
        continue;
    }

    const pathMatch = line.match(/^  (\/[^:]+):$/);
    if (pathMatch) {
        currentPath = pathMatch[1];
        if (!currentPath.startsWith(PLUGIN_ROUTE_PREFIX)) {
            console.error(`OpenAPI path must start with ${PLUGIN_ROUTE_PREFIX}: ${currentPath}`);
            process.exit(1);
        }
        continue;
    }

    const pathChildMatch = line.match(/^    ([a-zA-Z0-9.$_-]+):$/);
    if (pathChildMatch && currentPath) {
        const key = pathChildMatch[1];
        const kl = key.toLowerCase();
        if (HTTP_METHODS.has(kl)) {
            documented.add(`${kl.toUpperCase()} ${currentPath}`);
        } else if (key.startsWith('x-')) {
            // vendor extension field on the path item
        } else if (!ALLOWED_PATH_ITEM_KEYS.has(kl)) {
            console.error(`Unexpected OpenAPI path-item field "${key}" for ${currentPath}`);
            process.exit(1);
        }
    }
}

const expectedSet = new Set(expected.map(([method, route]) => `${method} ${route}`));
const missing = expected.
    map(([method, route]) => `${method} ${route}`).
    filter((route) => !documented.has(route));
const unexpected = [...documented].filter((route) => !expectedSet.has(route));

if (missing.length > 0 || unexpected.length > 0) {
    if (missing.length > 0) {
        console.error('OpenAPI spec is missing documented routes:');
        for (const route of missing) {
            console.error(`  - ${route}`);
        }
    }
    if (unexpected.length > 0) {
        console.error('OpenAPI spec documents unexpected routes:');
        for (const route of unexpected) {
            console.error(`  - ${route}`);
        }
    }
    process.exit(1);
}

console.log(`OpenAPI route coverage OK (${documented.size} operations, matches core routes).`);

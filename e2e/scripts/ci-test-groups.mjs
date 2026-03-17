// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.

import fs from 'fs';
import path from 'path';
import {fileURLToPath} from 'url';

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const e2eRoot = path.resolve(scriptDirectory, '..');
const testsRoot = path.resolve(e2eRoot, 'tests');

const realAPISpecs = new Set([
    'tests/channel-analysis/backend-verification/real-api.spec.ts',
    'tests/multiplayer-tool-calling/multiplayer-tool-calling.spec.ts',
    'tests/llmbot-post-component/citations-annotations.spec.ts',
    'tests/llmbot-post-component/combined-features.spec.ts',
    'tests/llmbot-post-component/debug-test.spec.ts',
    'tests/llmbot-post-component/edge-cases.spec.ts',
    'tests/llmbot-post-component/reasoning-display.spec.ts',
    'tests/llmbot-post-component/streaming-persistence.spec.ts',
]);

const groups = {
    'e2e-shard-1': [
        'tests/system-console/bot-reasoning-config.spec.ts',
        'tests/channel-analysis/integration/integration.spec.ts',
        'tests/bot-configuration/service-changes.spec.ts',
        'tests/long-conversation-handling/pagination.spec.ts',
        'tests/action-item-extraction/ui-verification.spec.ts',
        'tests/action-item-extraction/follow-ups.spec.ts',
        'tests/semantic-search/search-bot-selector.spec.ts',
        'tests/rhs-core/new-messages-rhs.spec.ts',
    ],
    'e2e-shard-2': [
        'tests/system-console/bot-validation.spec.ts',
        'tests/channel-analysis/quick-actions/quick-actions.spec.ts',
        'tests/system-console/initial-state-navigation.spec.ts',
        'tests/advanced-error-scenarios/network-errors.spec.ts',
        'tests/smart-reactions/basic-reactions.spec.ts',
        'tests/semantic-search/search-sources.spec.ts',
        'tests/bot-configuration/reasoning-config.spec.ts',
        'tests/action-item-extraction/error-handling.spec.ts',
        'tests/multiple-bot-conversations/bot-switching.spec.ts',
        'tests/agents-tour/display-conditions.spec.ts',
    ],
    'e2e-shard-3': [
        'tests/system-console/bot-native-tools.spec.ts',
        'tests/channel-analysis/edge-cases/edge-cases.spec.ts',
        'tests/channel-analysis/custom-queries/custom-queries.spec.ts',
        'tests/edge-cases/input-validation.spec.ts',
        'tests/bot-configuration/bot-identity-changes.spec.ts',
        'tests/agents-tour/basic-flow.spec.ts',
        'tests/semantic-search/search-citations.spec.ts',
        'tests/action-item-extraction/basic-action-items.spec.ts',
        'tests/action-item-extraction/edge-cases.spec.ts',
        'tests/agents-tour/edge-cases.spec.ts',
    ],
    'e2e-shard-4': [
        'tests/channel-analysis/response-citations/response-citations.spec.ts',
        'tests/system-console/debug-panel.spec.ts',
        'tests/system-console/functions-panel.spec.ts',
        'tests/rhs-core/basic.spec.ts',
        'tests/system-console/mcp-panel.spec.ts',
        'tests/system-console/service-management-ui.spec.ts',
        'tests/rhs-core/direct-channel-creation.spec.ts',
        'tests/system-console/bot-management-ui.spec.ts',
        'tests/semantic-search/search-entry-points.spec.ts',
        'tests/agents-tour/no-bots.spec.ts',
        'tests/channel-summarization/basic-summarization.spec.ts',
        'tests/seed.spec.ts',
    ],
    'llmbot-real-citations': [
        'tests/llmbot-post-component/citations-annotations.spec.ts',
        'tests/llmbot-post-component/combined-features.spec.ts',
    ],
    'llmbot-real-reasoning': [
        'tests/llmbot-post-component/reasoning-display.spec.ts',
        'tests/llmbot-post-component/streaming-persistence.spec.ts',
        'tests/llmbot-post-component/debug-test.spec.ts',
    ],
    'llmbot-real-edge-cases': [
        'tests/llmbot-post-component/edge-cases.spec.ts',
    ],
    'tool-calling-real': [
        'tests/multiplayer-tool-calling/multiplayer-tool-calling.spec.ts',
    ],
    'channel-analysis-real': [
        'tests/channel-analysis/backend-verification/real-api.spec.ts',
    ],
};

function walkSpecFiles(dirPath) {
    const entries = fs.readdirSync(dirPath, {withFileTypes: true});
    return entries.flatMap((entry) => {
        const absolutePath = path.join(dirPath, entry.name);
        if (entry.isDirectory()) {
            return walkSpecFiles(absolutePath);
        }

        if (!entry.name.endsWith('.spec.ts')) {
            return [];
        }

        return [path.relative(e2eRoot, absolutePath).replaceAll(path.sep, '/')];
    });
}

function flattenGroupSelection(groupNames) {
    return groupNames.flatMap((groupName) => {
        const files = groups[groupName];
        if (!files) {
            throw new Error(`Unknown CI test group: ${groupName}`);
        }

        return files;
    });
}

function getNonRealAPISpecs() {
    return walkSpecFiles(testsRoot)
        .filter((spec) => !realAPISpecs.has(spec))
        .sort();
}

function getRealAPISpecs() {
    return [...realAPISpecs].sort();
}

function validateUniqueFiles(label, files) {
    const seen = new Set();
    const duplicates = new Set();

    for (const file of files) {
        if (seen.has(file)) {
            duplicates.add(file);
        }
        seen.add(file);
    }

    if (duplicates.size > 0) {
        throw new Error(`${label} contains duplicate files:\n${[...duplicates].sort().join('\n')}`);
    }
}

function validateExistingFiles(files) {
    const missingFiles = files.filter((file) => !fs.existsSync(path.resolve(e2eRoot, file)));
    if (missingFiles.length > 0) {
        throw new Error(`Missing files in CI groups:\n${missingFiles.join('\n')}`);
    }
}

function validateCoverage() {
    const nonRealGroupNames = Object.keys(groups).filter((groupName) => groupName.startsWith('e2e-shard-'));
    const selectedNonRealSpecs = flattenGroupSelection(nonRealGroupNames).sort();
    const actualNonRealSpecs = getNonRealAPISpecs();

    validateExistingFiles(selectedNonRealSpecs);
    validateUniqueFiles('Non-real-api e2e shards', selectedNonRealSpecs);

    const missingNonRealSpecs = actualNonRealSpecs.filter((spec) => !selectedNonRealSpecs.includes(spec));
    if (missingNonRealSpecs.length > 0) {
        throw new Error(`Non-real-api shards are missing specs:\n${missingNonRealSpecs.join('\n')}`);
    }

    const extraNonRealSpecs = selectedNonRealSpecs.filter((spec) => !actualNonRealSpecs.includes(spec));
    if (extraNonRealSpecs.length > 0) {
        throw new Error(`Non-real-api shards include unexpected specs:\n${extraNonRealSpecs.join('\n')}`);
    }

    const selectedRealSpecs = flattenGroupSelection([
        'llmbot-real-citations',
        'llmbot-real-reasoning',
        'llmbot-real-edge-cases',
        'tool-calling-real',
        'channel-analysis-real',
    ]).sort();

    validateExistingFiles(selectedRealSpecs);
    validateUniqueFiles('Real-api e2e groups', selectedRealSpecs);

    const actualRealSpecs = getRealAPISpecs();
    const missingRealSpecs = actualRealSpecs.filter((spec) => !selectedRealSpecs.includes(spec));
    if (missingRealSpecs.length > 0) {
        throw new Error(`Real-api groups are missing specs:\n${missingRealSpecs.join('\n')}`);
    }

    const extraRealSpecs = selectedRealSpecs.filter((spec) => !actualRealSpecs.includes(spec));
    if (extraRealSpecs.length > 0) {
        throw new Error(`Real-api groups include unexpected specs:\n${extraRealSpecs.join('\n')}`);
    }
}

function printUsage() {
    console.error('Usage: node scripts/ci-test-groups.mjs <list|validate|groups> [group-name ...]');
}

function main() {
    const [, , command, ...rest] = process.argv;

    if (!command) {
        printUsage();
        process.exit(1);
    }

    switch (command) {
    case 'list': {
        if (rest.length === 0) {
            throw new Error('At least one group name is required for "list".');
        }

        const files = flattenGroupSelection(rest);
        validateExistingFiles(files);
        validateUniqueFiles('Selected group list', files);

        for (const file of files) {
            console.log(file);
        }
        break;
    }
    case 'groups':
        Object.keys(groups).sort().forEach((groupName) => {
            console.log(groupName);
        });
        break;
    case 'validate':
        validateCoverage();
        console.log('CI test groups are valid.');
        break;
    default:
        throw new Error(`Unknown command: ${command}`);
    }
}

main();

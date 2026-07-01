import {
    buildCombinedReasoningCitationResponse,
    buildRegenerateCitationResponse,
    mergeFixtureFiles,
} from '../../helpers/aimock-fixtures';

export const REASONING_CITATIONS_PROMPT = 'llmbot-combined-reasoning-citations-001';
export const REGENERATE_CITATIONS_PROMPT = 'llmbot-combined-regenerate-citations-001';

export const COMBINED_REASONING_TEXT =
    'Review TypeScript docs and note static typing benefits before summarizing.';

export const COMBINED_CITATION_CONTENT =
    'TypeScript improves maintainability with static types !!CITE1!! in large codebases.';

export const REGENERATE_REASONING_TEXT =
    'Compare TypeScript benefits and gather one authoritative source before answering.';

export const REGENERATE_CITATION_CONTENT =
    'TypeScript helps teams catch errors early with static types !!CITE1!! during development.';

export function buildLLMBotCombinedFeaturesFixtures() {
    return mergeFixtureFiles(
        buildCombinedReasoningCitationResponse({
            userMessage: REASONING_CITATIONS_PROMPT,
            toolCallId: 'call_combined_reasoning_001',
            searchQuery: 'typescript documentation benefits',
            reasoning: COMBINED_REASONING_TEXT,
            content: COMBINED_CITATION_CONTENT,
            title: 'Reasoning with citations',
        }),
        buildRegenerateCitationResponse({
            userMessage: REGENERATE_CITATIONS_PROMPT,
            toolCallId: 'call_combined_regenerate_001',
            regenerateToolCallId: 'call_combined_regenerate_002',
            searchQuery: 'typescript development benefits',
            reasoning: REGENERATE_REASONING_TEXT,
            content: REGENERATE_CITATION_CONTENT,
            title: 'Regenerate with citations',
        }),
    );
}

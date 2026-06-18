import {
    buildWebSearchCitationSequence,
    mergeFixtureFiles,
} from '../../helpers/aimock-fixtures';

export const CITATION_DISPLAY_PROMPT = 'llmbot-citation-display-001';
export const CITATION_TOOLTIP_PROMPT = 'llmbot-citation-tooltip-001';
export const CITATION_CLICK_PROMPT = 'llmbot-citation-click-001';
export const MULTIPLE_CITATIONS_PROMPT = 'llmbot-citation-multiple-001';
export const CITATION_PERSISTENCE_PROMPT = 'llmbot-citation-persistence-001';
export const CITATION_MARKDOWN_PROMPT = 'llmbot-citation-markdown-001';
export const CITATION_INLINE_PROMPT = 'llmbot-citation-inline-001';
export const CITATION_FAVICON_PROMPT = 'llmbot-citation-favicon-001';

const SINGLE_CITATION_CONTENT =
    'TypeScript adds optional static typing !!CITE1!! for safer JavaScript development.';

const MULTIPLE_CITATION_CONTENT =
    'TypeScript adds static types !!CITE1!! while JavaScript remains dynamic !!CITE2!! across many projects.';

const INLINE_CITATION_CONTENT =
    'TypeScript improves large apps with types !!CITE1!! and JavaScript keeps flexibility !!CITE2!! for quick scripts.';

const MARKDOWN_CITATION_CONTENT =
    'Key TypeScript features include:\n- Structural typing\n- `interface` declarations\nSee docs !!CITE1!! for examples.';

export function buildLLMBotCitationsFixtures() {
    return mergeFixtureFiles(
        buildWebSearchCitationSequence({
            userMessage: CITATION_DISPLAY_PROMPT,
            toolCallId: 'call_citation_display_001',
            searchQuery: 'typescript documentation',
            content: SINGLE_CITATION_CONTENT,
            title: 'TypeScript citation display',
        }),
        buildWebSearchCitationSequence({
            userMessage: CITATION_TOOLTIP_PROMPT,
            toolCallId: 'call_citation_tooltip_001',
            searchQuery: 'typescript documentation',
            content: SINGLE_CITATION_CONTENT,
            title: 'TypeScript citation tooltip',
        }),
        buildWebSearchCitationSequence({
            userMessage: CITATION_CLICK_PROMPT,
            toolCallId: 'call_citation_click_001',
            searchQuery: 'typescript official site',
            content: 'The official TypeScript site is cited here !!CITE1!!.',
            title: 'TypeScript citation click',
        }),
        buildWebSearchCitationSequence({
            userMessage: MULTIPLE_CITATIONS_PROMPT,
            toolCallId: 'call_citation_multiple_001',
            searchQuery: 'typescript javascript react comparison',
            content: MULTIPLE_CITATION_CONTENT,
            title: 'Multiple citations',
        }),
        buildWebSearchCitationSequence({
            userMessage: CITATION_PERSISTENCE_PROMPT,
            toolCallId: 'call_citation_persistence_001',
            searchQuery: 'typescript documentation overview',
            content: SINGLE_CITATION_CONTENT,
            title: 'Citation persistence',
        }),
        buildWebSearchCitationSequence({
            userMessage: CITATION_MARKDOWN_PROMPT,
            toolCallId: 'call_citation_markdown_001',
            searchQuery: 'typescript code examples',
            content: MARKDOWN_CITATION_CONTENT,
            title: 'Citation markdown',
        }),
        buildWebSearchCitationSequence({
            userMessage: CITATION_INLINE_PROMPT,
            toolCallId: 'call_citation_inline_001',
            searchQuery: 'typescript javascript inline citations',
            content: INLINE_CITATION_CONTENT,
            title: 'Inline citations',
        }),
        buildWebSearchCitationSequence({
            userMessage: CITATION_FAVICON_PROMPT,
            toolCallId: 'call_citation_favicon_001',
            searchQuery: 'typescript official documentation',
            content: SINGLE_CITATION_CONTENT,
            title: 'Citation favicon',
        }),
    );
}

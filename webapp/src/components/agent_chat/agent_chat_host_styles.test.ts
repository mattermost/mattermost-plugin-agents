// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    TEXTAREA_EXPANDED_MAX_HEIGHT,
    TEXTAREA_EXPANDED_ONE_LINE_HEIGHT,
    TEXTAREA_MAX_HEIGHT,
    TEXTAREA_ONE_LINE_HEIGHT,
    composerTextStyles,
    hostTextboxPillStyles,
} from './agent_chat_composer_host_styles';
import {CHAT_CONTENT_MAX_WIDTH, hostThreadChatStyles} from './agent_chat_host_styles';

// jsdom never applies stylesheets, so check the generated rules instead.
const cssText = (value: unknown): string => {
    if (Array.isArray(value)) {
        return value.map(cssText).join('');
    }
    return typeof value === 'string' ? value : '';
};

// The declarations of the first rule whose selector list includes `selector`.
const ruleFor = (text: string, selector: string) => {
    const rules = text.replace(/\/\*[\s\S]*?\*\//g, '').split('}');
    const rule = rules.find((r) => (r.split('{')[0] ?? '').split(',').map((s) => s.trim()).includes(selector));
    return rule?.split('{')[1]?.replace(/\s+/g, ' ').trim() ?? null;
};

describe('hostThreadChatStyles', () => {
    test.each([
        {name: 'page (product view)', options: {columnMaxWidth: CHAT_CONTENT_MAX_WIDTH}, capped: true},
        {name: 'panel (RHS)', options: {}, capped: false},
    ])('$name', ({options, capped}) => {
        const text = cssText(hostThreadChatStyles(options));

        // The host reply box and the "N replies" divider never show.
        expect(ruleFor(text, '[data-testid=\'comment-create\']')).toContain('display: none');
        expect(ruleFor(text, '.root-post__divider')).toContain('display: none');

        // Posts and separators share one column where there is a cap, and the full width otherwise.
        const column = `max-width: ${CHAT_CONTENT_MAX_WIDTH}`;
        if (capped) {
            expect(ruleFor(text, '.post')).toContain(column);
            expect(ruleFor(text, '.Separator')).toContain(column);
        } else {
            expect(text).not.toContain(column);
        }
    });
});

describe('hostTextboxPillStyles', () => {
    // Styled-components keeps `&` in interpolated css; the selectors here are as written.
    const text = cssText(hostTextboxPillStyles);

    test('the host textarea is one 40px line at least (an empty box never clips its placeholder) and grows to the cap', () => {
        // One line = line-height-100 + the block padding the text styles use.
        expect(TEXTAREA_ONE_LINE_HEIGHT).toBe('calc(var(--line-height-100) + 2 * var(--spacing-s))');
        expect(composerTextStyles).toContain('line-height: var(--line-height-100);');
        expect(composerTextStyles).toContain('padding: var(--spacing-s) 0;');

        const textarea = ruleFor(text, '&&& textarea.custom-textarea');
        expect(textarea).toContain(`min-height: ${TEXTAREA_ONE_LINE_HEIGHT}`);
        expect(textarea).toContain(`max-height: ${TEXTAREA_MAX_HEIGHT}`);

        // The textarea and its autosize copy share the text metrics, so the copy measures what the box shows.
        const shared = ruleFor(text, '&&& .custom-textarea');
        expect(shared).toContain('line-height: var(--line-height-100)');
        expect(shared).toContain('padding: var(--spacing-s) 0');
    });

    test('expanded, the host textarea and its sizing copy are inset by spacing-xs on every side', () => {
        expect(ruleFor(text, '&&&[data-expanded=\'true\'] .custom-textarea')).toBe('padding: var(--spacing-xs);');
        const textarea = ruleFor(text, '&&&[data-expanded=\'true\'] textarea.custom-textarea');
        expect(textarea).toContain(`min-height: ${TEXTAREA_EXPANDED_ONE_LINE_HEIGHT}`);
        expect(textarea).toContain(`max-height: ${TEXTAREA_EXPANDED_MAX_HEIGHT}`);
    });

    test('the suggestion list floats above the whole pill', () => {
        expect(ruleFor(text, '&&& .textarea-wrapper')).toContain('position: static');
        expect(ruleFor(text, '&&& .suggestion-list--top')).toContain('bottom: calc(100% + var(--spacing-xs))');
    });
});

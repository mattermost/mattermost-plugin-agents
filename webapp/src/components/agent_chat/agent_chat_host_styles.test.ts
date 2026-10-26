// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

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

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
import {CHAT_COLUMN_PADDING, CHAT_CONTENT_MAX_WIDTH, CHAT_PANEL_PADDING, HUG_MAX_FRACTION, hostThreadChatStyles} from './agent_chat_host_styles';

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

        // Date separators leave more room under the line than the host's own 1em.
        expect(text).toContain('margin-bottom: var(--spacing-xxl)');

        // Your own bubbles reserve the controls bar's row below them, for the time.
        expect(ruleFor(text, '.post.current--user .post__content > .post__img + div')).toContain('margin-bottom: calc(28px + var(--spacing-xxxxs))');

        // Posts and separators share one column where there is a cap, and the full width otherwise.
        const column = `max-width: ${CHAT_CONTENT_MAX_WIDTH}`;
        if (capped) {
            // A classic scrollbar must not pull the column off the composer's center.
            expect(ruleFor(text, '#threadViewerScrollContainer')).toContain('scrollbar-gutter: stable both-edges');

            // The cap and centering are on the list's inner wrapper, not on each row.
            expect(ruleFor(text, '.innerList')).toContain(column);
            expect(ruleFor(text, '.post')).toBeNull();

            // The column is inset by 12px (--spacing-m) on each side.
            expect(ruleFor(text, '.innerList')).toContain('width: calc(100% - 2 * var(--spacing-m))');

            // The wrapper's side padding is the composer's (agent_chat_thread_view.tsx), so they line up;
            // the rows fill its content box with no padding of their own.
            expect(ruleFor(text, '.innerList')).toContain(`padding-inline: ${CHAT_COLUMN_PADDING}`);
            expect(ruleFor(text, '&&& .post')).toContain('padding-inline: 0');
            expect(ruleFor(text, '&&& .Separator')).toContain('padding-inline: 0');
        } else {
            expect(text).not.toContain(column);
            expect(text).not.toContain('scrollbar-gutter');

            // The rows still share the composer's side padding, with no cap or inset.
            expect(ruleFor(text, '.innerList')).toBe(`box-sizing: border-box; padding-inline: ${CHAT_PANEL_PADDING};`);
            expect(ruleFor(text, '&&& .post')).toContain('padding-inline: 0');
            expect(text).not.toContain('2 * var(--spacing-m)');
        }
    });
});

describe('hostTextboxPillStyles', () => {
    // Styled-components keeps `&` in interpolated css; the selectors here are as written.
    const text = cssText(hostTextboxPillStyles);

    test('the host textarea is one 32px line at least (an empty box never clips its placeholder) and grows to the cap', () => {
        // One line = line-height-100 + the block padding the text styles use.
        expect(TEXTAREA_ONE_LINE_HEIGHT).toBe('calc(var(--line-height-100) + 2 * var(--spacing-xxs))');
        expect(composerTextStyles).toContain('line-height: var(--line-height-100);');
        expect(composerTextStyles).toContain('padding: var(--spacing-xxs) 0;');

        const textarea = ruleFor(text, '&&& textarea.custom-textarea');
        expect(textarea).toContain(`min-height: ${TEXTAREA_ONE_LINE_HEIGHT}`);
        expect(textarea).toContain(`max-height: ${TEXTAREA_MAX_HEIGHT}`);

        // The textarea and its autosize copy share the text metrics, so the copy measures what the box shows.
        const shared = ruleFor(text, '&&& .custom-textarea');
        expect(shared).toContain('line-height: var(--line-height-100)');
        expect(shared).toContain('padding: var(--spacing-xxs) 0');
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

describe('hostThreadChatStyles bubble fit', () => {
    const text = cssText(hostThreadChatStyles());
    const bubble = '.post .post__content > .post__img + div';

    test('an agent bubble marked hug shrinks to its content, capped at the hug fraction of the row', () => {
        expect(HUG_MAX_FRACTION).toBe(0.75);
        const rule = ruleFor(text, `${bubble}:has([data-bubble-fit='hug'])`);
        expect(rule).toContain('flex: 0 1 auto');
        expect(rule).toContain('width: fit-content');
        expect(rule).toContain(`max-width: calc(${HUG_MAX_FRACTION} * 100%)`);
    });

    // The fit is decided from the bubble's measured width: a width mid-transition
    // reads as pinned at the cap, and hug and full flip every frame.
    test('bubble width changes never transition', () => {
        expect(text).not.toMatch(/transition:[^;]*\b(width|max-width|flex-grow|min-width)\b/);
        expect(text).not.toContain('interpolate-size');
    });

    test('agent bubbles default to the full row, own bubbles are unchanged', () => {
        expect(ruleFor(text, bubble)).toContain('flex: 1 1 auto');
        expect(ruleFor(text, '.post.current--user .post__content > .post__img + div')).toContain('flex: 0 1 auto');
    });
});

describe('hostThreadChatStyles controls below the bubble', () => {
    const text = cssText(hostThreadChatStyles());
    const bubble = '.post .post__content > .post__img + div';

    test('only a bubble holding the below marker reserves space, from tokens plus the bar height', () => {
        const rule = ruleFor(text, `${bubble}:has([data-bubble-controls='below'])`);
        expect(rule).toContain('margin-bottom: calc(28px + var(--spacing-xxxxs))');
        expect(ruleFor(text, bubble)).not.toContain('margin-bottom');
    });

    test('the marked bar is placed absolutely under the reply, aligned with its text', () => {
        const rule = ruleFor(text, `${bubble} [data-bubble-controls='below']`);
        expect(rule).toContain('position: absolute');
        expect(rule).toContain('top: calc(100% + var(--spacing-xxxxs))');
        expect(rule).toContain('left: var(--spacing-l)');
    });
});

describe('hostThreadChatStyles message look', () => {
    const text = cssText(hostThreadChatStyles());
    const ownBubble = '.post.current--user .post__content > .post__img + div';

    test('neither the agent nor the user shows an avatar', () => {
        expect(ruleFor(text, '.post .post__content > .post__img')).toContain('display: none');
    });

    test('the agent\'s reply keeps the bubble shape, tinted only on hover, and hides its header except the toolbar', () => {
        const rule = ruleFor(text, '.post .post__content > .post__img + div');
        expect(rule).toContain('padding: var(--spacing-xs) var(--spacing-l)');
        expect(rule).toContain('background: transparent');
        expect(ruleFor(text, '.post .post__content > .post__img + div:hover')).toContain('background: rgba(var(--center-channel-color-rgb), 0.08)');
        expect(ruleFor(text, '.post:not(.current--user) .post__header > :not(.post-menu)')).toContain('display: none !important');
    });

    test('both bubbles round all four corners the same, as a squircle', () => {
        [ruleFor(text, '.post .post__content > .post__img + div'), ruleFor(text, ownBubble)].forEach((rule) => {
            expect(rule).toContain('border-radius: var(--radius-xl)');
            expect(rule).toContain('corner-shape: squircle');
        });
    });

    test('your own bubble reverses the colors, hides your name and puts the time under the bubble', () => {
        const rule = ruleFor(text, ownBubble);
        expect(rule).toContain('background: var(--center-channel-color)');
        expect(rule).toContain('color: var(--center-channel-bg)');
        expect(ruleFor(text, '.post.current--user .post__header .col__name')).toContain('display: none');
        const time = ruleFor(text, '.post.current--user .post__header .badges-wrapper');
        expect(time).toContain('position: absolute');
        expect(time).toContain('top: calc(100% + var(--spacing-xxxxs))');
    });
});

describe('hostThreadChatStyles post content', () => {
    test('the host\'s side padding on the post content is removed so posts sit centered', () => {
        const text = cssText(hostThreadChatStyles());

        expect(ruleFor(text, '&&& .post .post__content')).toContain('padding: 0');
    });
});

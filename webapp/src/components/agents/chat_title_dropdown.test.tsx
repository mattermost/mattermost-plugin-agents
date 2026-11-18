// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import fs from 'fs';
import path from 'path';

import {render, screen} from '@testing-library/react';

import ChatTitleDropdown from './chat_title_dropdown';

const compassCss = (rel: string) => fs.readFileSync(path.resolve(__dirname, '../../../node_modules/@mattermost/compass-ui/dist/components', rel), 'utf8');

// Button's medium padding and quaternary hover tie Dropdown's large padding and neutral hover.
// Loading Button after Dropdown is the order where Button wins unless this component outranks it.
const installLosingOrder = () => {
    const style = document.createElement('style');
    style.textContent = [
        ':root{--spacing-xxxxs:2px;--spacing-xxxs:4px;--spacing-xxs:6px;--spacing-s:12px;--spacing-xl:24px;--center-channel-color-rgb:61,60,64;--link-color-rgb:28,88,217;}',
        compassCss('dropdown/Dropdown.css'),
        compassCss('button/Button.css'),
    ].join('\n');
    document.head.append(style);
    return () => style.remove();
};

const splitTop = (value: string) => {
    const parts: string[] = [];
    let depth = 0;
    let current = '';
    for (const ch of value) {
        if (ch === '(') {
            depth++;
        } else if (ch === ')') {
            depth--;
        } else if (ch === ',' && depth === 0) {
            parts.push(current);
            current = '';
            continue;
        }
        current += ch;
    }
    parts.push(current);
    return parts;
};

// Specificity of the class/pseudo column. :not() counts its most specific argument, as browsers do.
const specificity = (selector: string) => {
    const count = (value: string) => (value.match(/\.[_A-Za-z0-9-]+|:(?:hover|active|disabled|focus-visible)/g) || []).length;
    const withoutNot = selector.replace(/:not\([^)]*\)/g, '');
    const notArgs = [...selector.matchAll(/:not\(([^)]*)\)/g)].map((match) => match[1]);
    const notSpec = notArgs.reduce((max, arg) => Math.max(max, ...splitTop(arg).map(count)), 0);
    return count(withoutNot) + notSpec;
};

// The background-color that wins for this element while hovered.
const winningHoverBackground = (element: Element) => {
    let best = '';
    let bestSpec = -1;
    let bestOrder = -1;
    let order = 0;
    for (const sheet of document.styleSheets) {
        for (const rule of sheet.cssRules) {
            order++;
            if (!(rule instanceof CSSStyleRule)) {
                continue;
            }
            const background = (/background-color:\s*([^;]+)/).exec(rule.cssText)?.[1]?.trim() ?? '';
            if (!background) {
                continue;
            }
            for (const part of splitTop(rule.selectorText)) {
                if (!part.includes(':hover')) {
                    continue;
                }
                const resting = part.replace(/:hover/g, '').trim();
                if (!resting || !element.matches(resting)) {
                    continue;
                }
                const spec = specificity(part);
                if (spec > bestSpec || (spec === bestSpec && order > bestOrder)) {
                    bestSpec = spec;
                    bestOrder = order;
                    best = background;
                }
            }
        }
    }
    return best;
};

describe('ChatTitleDropdown', () => {
    it('keeps the Dropdown padding and hover when the Button stylesheet loads last', () => {
        const cleanup = installLosingOrder();
        render(
            <ChatTitleDropdown
                label='Q3 plan'
                ariaLabel='Q3 plan options'
                expanded={false}
                onClick={jest.fn()}
                testId='agent-chat-menu-button'
                heading={true}
            />,
        );
        const trigger = screen.getByTestId('agent-chat-menu-button');

        // Dropdown large (2px 4px 2px 6px). Button medium would be var(--spacing-s) var(--spacing-xl).
        expect(getComputedStyle(trigger).padding).toBe('var(--spacing-xxxxs) var(--spacing-xxxs) var(--spacing-xxxxs) var(--spacing-xxs)');
        expect(winningHoverBackground(trigger)).toBe('rgba(var(--center-channel-color-rgb), 0.08)');
        cleanup();
    });
});

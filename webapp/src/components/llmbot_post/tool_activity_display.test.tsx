// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {ToolCallStatus} from '../tool_types';
import en from '../../i18n/en.json';

import {deriveActivity} from './activity_items';
import {advanceAnimation, makeRound, makeServerTool, makeTool} from './test_support';
import ToolActivityDisplay from './tool_activity_display';
import type {Round} from './turn_content_utils';

// Rendered against the shipped en.json: at runtime registered messages win over defaultMessage.
function activityElement(rounds: Round[], options: {
    expanded?: boolean;
    inProgress?: boolean;
    onToggleExpanded?: (expanded: boolean) => void;
} = {}) {
    return (
        <IntlProvider
            locale='en'
            messages={en}
        >
            <ToolActivityDisplay
                activity={deriveActivity(rounds)}
                expanded={options.expanded ?? false}
                onToggleExpanded={options.onToggleExpanded ?? jest.fn()}
                inProgress={options.inProgress ?? false}
                renderRound={(round) => <div key={round.id}>{`round:${round.id}`}</div>}
            />
        </IntlProvider>
    );
}

const currentRow = () => screen.getByTestId('llm-bot-tool-activity-current');

describe('ToolActivityDisplay collapsed row', () => {
    test('shows only the summary once the response is done', () => {
        render(activityElement([
            makeRound('r1', 'Let me look', [makeTool({id: 'tc_a'}), makeTool({id: 'tc_b'})]),
            makeRound('r2', '', [], [makeServerTool({id: 'srv_a'})]),
        ]));

        expect(currentRow().textContent).toBe('Used 3 tools');
        expect(screen.queryByText('round:r1')).toBeNull();
    });

    test.each([
        {
            name: 'a running client tool',
            rounds: [makeRound('r1', '', [makeTool({id: 'tc_a'}), makeTool({id: 'tc_b', name: 'load_tool', status: ToolCallStatus.Pending})])],
            inProgress: true,
            expected: 'Load Tool',
        },
        {
            name: 'a call still pending after the stream stopped',
            rounds: [makeRound('r1', '', [makeTool({id: 'tc_a', name: 'create_post', status: ToolCallStatus.Pending})])],
            inProgress: false,
            expected: 'Create Post',
        },
    ])('shows the latest item for $name', ({rounds, inProgress, expected}) => {
        render(activityElement(rounds, {inProgress}));

        expect(currentRow().textContent).toBe(expected);
    });

    test.each([
        {name: 'a failed client tool', rounds: [makeRound('r1', '', [makeTool({status: ToolCallStatus.Error})])], status: 'error'},
        {name: 'a failed provider tool', rounds: [makeRound('r1', '', [], [makeServerTool({status: 'error'})])], status: 'error'},
        {name: 'a rejected call', rounds: [makeRound('r1', '', [makeTool({status: ToolCallStatus.Rejected})])], status: 'rejected'},
        {name: 'all successful', rounds: [makeRound('r1', '', [makeTool()])], status: 'success'},
    ])('reports the outcome of $name in the summary glyph', ({rounds, status}) => {
        render(activityElement(rounds));

        expect(screen.getByTestId('llm-bot-tool-status').getAttribute('data-status')).toBe(status);
    });
});

describe('ToolActivityDisplay expand and collapse', () => {
    beforeEach(() => {
        jest.useFakeTimers();
    });

    afterEach(() => {
        jest.clearAllTimers();
        jest.useRealTimers();
    });

    const rounds = [
        makeRound('r1', 'Let me look', [makeTool({id: 'tc_a'})]),
        makeRound('r2', '', [makeTool({id: 'tc_b'})]),
    ];

    test('reveals the stacked rounds and removes them once the collapse finishes', () => {
        const onToggleExpanded = jest.fn();
        const {rerender} = render(activityElement(rounds, {onToggleExpanded}));

        const header = screen.getByTestId('llm-bot-tool-activity-header');
        expect(header.getAttribute('aria-expanded')).toBe('false');
        fireEvent.click(header);
        expect(onToggleExpanded).toHaveBeenLastCalledWith(true);

        rerender(activityElement(rounds, {onToggleExpanded, expanded: true}));
        expect(screen.getByText('round:r1')).toBeTruthy();
        expect(screen.getByText('round:r2')).toBeTruthy();

        fireEvent.click(screen.getByTestId('llm-bot-tool-activity-header'));
        expect(onToggleExpanded).toHaveBeenLastCalledWith(false);
        rerender(activityElement(rounds, {onToggleExpanded, expanded: false}));

        expect(screen.getByText('round:r1')).toBeTruthy();
        advanceAnimation();
        expect(screen.queryByText('round:r1')).toBeNull();
    });
});

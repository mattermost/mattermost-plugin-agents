// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {ToolCallStatus} from '../tool_types';
import en from '../../i18n/en.json';

import {deriveActivity} from './activity_items';
import {advanceAnimation, makeRound, makeServerTool, makeTool, withReasoning} from './test_support';
import ToolActivityDisplay from './tool_activity_display';
import type {Round} from './turn_content_utils';

// Rendered against the shipped en.json: at runtime registered messages win over defaultMessage.
function activityElement(rounds: Round[], options: {
    expanded?: boolean;
    inProgress?: boolean;
    working?: boolean;
    statusMessage?: string;
    reasoningLoadingRoundId?: string;
    onToggleExpanded?: (expanded: boolean) => void;
} = {}) {
    return (
        <IntlProvider
            locale='en'
            messages={en}
        >
            <ToolActivityDisplay
                activity={deriveActivity(rounds, {reasoningLoadingRoundId: options.reasoningLoadingRoundId})}
                expanded={options.expanded ?? false}
                onToggleExpanded={options.onToggleExpanded ?? jest.fn()}
                inProgress={options.inProgress ?? false}
                working={options.working ?? false}
                statusMessage={options.statusMessage}
                renderRound={(round) => <div key={round.id}>{`round:${round.id}`}</div>}
            />
        </IntlProvider>
    );
}

const currentRow = () => screen.getByTestId('llm-bot-tool-activity-current');

describe('ToolActivityDisplay collapsed row', () => {
    test('summarizes a finished response even if a provider tool never reported completion', () => {
        render(activityElement([
            makeRound('r1', 'Let me look', [makeTool({id: 'tc_a'})], [makeServerTool({id: 'srv_a', status: 'in_progress'})]),
        ]));

        expect(currentRow().textContent).toBe('Used 2 tools');
    });

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

    test('shows Thinking with a spinner while reasoning after a tool streams', () => {
        render(activityElement(
            [makeRound('r1', '', [makeTool()]), withReasoning(makeRound('r2', ''))],
            {inProgress: true, working: true, reasoningLoadingRoundId: 'r2'},
        ));

        expect(currentRow().textContent).toBe('Thinking');
        expect(within(currentRow()).getByTestId('llm-bot-activity-spinner')).toBeTruthy();
    });

    test('shows a spinner instead of the finished tool glyph while the response is generating', () => {
        render(activityElement([makeRound('r1', '', [makeTool({name: 'read_channel'})])], {inProgress: true, working: true}));

        expect(currentRow().textContent).toBe('Read Channel');
        expect(within(currentRow()).getByTestId('llm-bot-activity-spinner')).toBeTruthy();
        expect(within(currentRow()).queryByTestId('llm-bot-tool-status')).toBeNull();
    });

    test('shows the finished tool glyph while paused on a decision', () => {
        render(activityElement([makeRound('r1', '', [makeTool({name: 'read_channel'})])], {inProgress: true}));

        expect(within(currentRow()).queryByTestId('llm-bot-activity-spinner')).toBeNull();
        expect(within(currentRow()).getByTestId('llm-bot-tool-status').getAttribute('data-status')).toBe('success');
    });

    test('counts only tools in the summary', () => {
        render(activityElement([
            withReasoning(makeRound('r1', '', [makeTool()])),
            withReasoning(makeRound('r2', 'Here is the answer')),
        ]));

        expect(currentRow().textContent).toBe('Used 1 tool');
    });

    test('shows a setup phase on a header that cannot expand yet', () => {
        const onToggleExpanded = jest.fn();
        render(activityElement([], {statusMessage: 'Connecting to provider...', working: true, onToggleExpanded}));

        expect(currentRow().textContent).toBe('Connecting to provider...');
        const header = screen.getByTestId('llm-bot-tool-activity-header');
        expect(header.hasAttribute('aria-expanded')).toBe(false);
        fireEvent.click(header);
        expect(onToggleExpanded).not.toHaveBeenCalled();
        expect(screen.queryByTestId('llm-bot-tool-activity-rounds')).toBeNull();
    });
});

describe('ToolActivityDisplay line transitions', () => {
    beforeEach(() => {
        jest.useFakeTimers();
    });

    afterEach(() => {
        jest.clearAllTimers();
        jest.useRealTimers();
    });

    const live = {inProgress: true, working: true};
    const firstTool = makeRound('r1', '', [makeTool({id: 'tc_a', name: 'read_channel'})]);
    const secondTool = makeRound('r2', '', [makeTool({id: 'tc_b', name: 'create_post'})]);

    test('rolls the previous line out while the next one rolls in', () => {
        const {rerender} = render(activityElement([firstTool], live));

        rerender(activityElement([firstTool, secondTool], live));

        expect(currentRow().textContent).toBe('Create Post');
        const outgoing = screen.getByText('Read Channel').closest('[aria-hidden="true"]');
        expect(outgoing).not.toBeNull();

        advanceAnimation();
        expect(screen.queryByText('Read Channel')).toBeNull();
    });

    test('keeps one header from the setup phase through the first tool', () => {
        const {rerender} = render(activityElement([], {...live, statusMessage: 'Connecting to provider...'}));
        const header = screen.getByTestId('llm-bot-tool-activity-header');

        rerender(activityElement([firstTool], live));

        expect(screen.getByTestId('llm-bot-tool-activity-header')).toBe(header);
        expect(header.getAttribute('aria-expanded')).toBe('false');
        expect(currentRow().textContent).toBe('Read Channel');
    });

    test('does not roll when a settled post finishes loading', () => {
        const {rerender} = render(activityElement([], {statusMessage: 'Working...'}));

        rerender(activityElement([firstTool]));

        expect(currentRow().textContent).toBe('Used 1 tool');
        expect(screen.queryByText('Working...')).toBeNull();
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

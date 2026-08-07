// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {ToolCall, ToolCallStatus} from '../tool_types';

import {deriveActivity} from './activity_items';
import ToolActivityDisplay from './tool_activity_display';
import type {Round} from './turn_content_utils';

function makeTool(overrides: Partial<ToolCall> = {}): ToolCall {
    return {
        id: 'tc_1',
        name: 'search_tools',
        description: '',
        status: ToolCallStatus.Success,
        ...overrides,
    };
}

function makeRound(id: string, text: string, toolCalls: ToolCall[] = []): Round {
    return {
        id,
        text,
        toolCalls,
        reasoning: {summary: '', signature: ''},
        annotations: [],
    };
}

function activityElement(rounds: Round[], options: {
    expanded?: boolean;
    inProgress?: boolean;
    onToggleExpanded?: (expanded: boolean) => void;
} = {}) {
    return (
        <IntlProvider locale='en'>
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

describe('ToolActivityDisplay collapsed rendering', () => {
    const finishedRounds = [
        makeRound('r1', 'Let me get the jira tools loaded', [
            makeTool({id: 'tc_a', name: 'search_tools'}),
            makeTool({id: 'tc_b', name: 'load_tool'}),
        ]),
        makeRound('r2', 'Now let me create the Jira ticket', [makeTool({id: 'tc_c', name: 'CreateJiraIssue'})]),
    ];

    test('shows only the summary row and hides the stacked rounds by default', () => {
        render(activityElement(finishedRounds));

        expect(screen.getByText('Used 3 tools')).toBeTruthy();
        expect(screen.queryByText('round:r1')).toBeNull();
        expect(screen.queryByText('round:r2')).toBeNull();
    });

    test('shows the running tool as the current item while the response is in progress', () => {
        render(activityElement(
            [makeRound('r1', 'Looking that up', [makeTool({id: 'tc_a', name: 'search_tools', status: ToolCallStatus.Pending})])],
            {inProgress: true},
        ));

        expect(screen.getByText('Search Tools')).toBeTruthy();
        expect(screen.queryByText('Used 1 tool')).toBeNull();
    });

    // A tool can still be running after the stream stops (e.g. a call waiting
    // on approval), so completion is not just "not generating".
    test('does not summarize while a tool call is still pending', () => {
        render(activityElement(
            [makeRound('r1', '', [makeTool({id: 'tc_a', name: 'create_post', status: ToolCallStatus.Pending})])],
            {inProgress: false},
        ));

        expect(screen.getByText('Create Post')).toBeTruthy();
        expect(screen.queryByText('Used 1 tool')).toBeNull();
    });

    test('reports the outcome in the summary when a tool failed', () => {
        render(activityElement([
            makeRound('r1', '', [makeTool({id: 'tc_a', status: ToolCallStatus.Error})]),
        ]));

        expect(screen.getByText('Used 1 tool')).toBeTruthy();
    });

    test('expands to the full stack of rounds when the header is clicked', () => {
        const onToggleExpanded = jest.fn();
        const {rerender} = render(activityElement(finishedRounds, {onToggleExpanded}));

        fireEvent.click(screen.getByTestId('llm-bot-tool-activity-header'));
        expect(onToggleExpanded).toHaveBeenCalledWith(true);

        rerender(activityElement(finishedRounds, {onToggleExpanded, expanded: true}));

        expect(screen.getByText('round:r1')).toBeTruthy();
        expect(screen.getByText('round:r2')).toBeTruthy();

        fireEvent.click(screen.getByTestId('llm-bot-tool-activity-header'));
        expect(onToggleExpanded).toHaveBeenLastCalledWith(false);
    });
});

describe('ToolActivityDisplay current item sequencing', () => {
    beforeEach(() => {
        jest.useFakeTimers();
    });

    afterEach(() => {
        jest.clearAllTimers();
        jest.useRealTimers();
    });

    function advanceBy(ms: number) {
        act(() => {
            jest.advanceTimersByTime(ms);
        });
    }

    // Advancing has to happen in two passes: the state update that swaps the
    // row only schedules the timer that retires the outgoing row once React
    // has flushed it.
    function advanceAnimation() {
        advanceBy(1000);
        advanceBy(1000);
    }

    const startingRounds = [
        makeRound('r1', 'Looking that up', [makeTool({id: 'tc_a', name: 'search_tools', status: ToolCallStatus.Pending})]),
    ];

    function rerenderWith(rerender: (ui: React.ReactElement) => void, rounds: Round[]) {
        rerender(activityElement(rounds, {inProgress: true}));
    }

    test('steps through newly appended items instead of skipping them', () => {
        const {rerender} = render(activityElement(startingRounds, {inProgress: true}));
        expect(screen.getByText('Search Tools')).toBeTruthy();

        rerenderWith(rerender, [makeRound('r1', 'Looking that up', [
            makeTool({id: 'tc_a', name: 'search_tools'}),
            makeTool({id: 'tc_b', name: 'load_tool', status: ToolCallStatus.Pending}),
        ])]);
        expect(screen.queryByText('Load Tool')).toBeNull();

        advanceAnimation();
        expect(screen.getByText('Load Tool')).toBeTruthy();
        expect(screen.queryByText('Search Tools')).toBeNull();
    });

    // Items can arrive faster than the row animates. Restarting the wait on
    // every arrival would freeze the row on its first item until the backlog
    // grew large enough to be skipped wholesale, so the wait has to carry
    // over the time already served.
    test('keeps stepping when items arrive faster than the step interval', () => {
        const {rerender} = render(activityElement(startingRounds, {inProgress: true}));

        advanceBy(100);
        rerenderWith(rerender, [makeRound('r1', 'Looking that up', [
            makeTool({id: 'tc_a', name: 'search_tools'}),
            makeTool({id: 'tc_b', name: 'load_tool', status: ToolCallStatus.Pending}),
        ])]);

        advanceBy(100);
        rerenderWith(rerender, [makeRound('r1', 'Looking that up', [
            makeTool({id: 'tc_a', name: 'search_tools'}),
            makeTool({id: 'tc_b', name: 'load_tool'}),
            makeTool({id: 'tc_c', name: 'list_channels', status: ToolCallStatus.Pending}),
        ])]);

        // 300ms in, past the 260ms step: the current row must have advanced
        // off the first item rather than still sitting on it. (The first item
        // is still in the DOM here as the row animating out.)
        advanceBy(100);
        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Load Tool');
    });

    // When updates arrive faster than the row can animate, showing a long
    // backlog would misreport what the agent is doing right now.
    test('jumps to the newest item when the backlog outruns the animation', () => {
        const {rerender} = render(activityElement(startingRounds, {inProgress: true}));

        rerenderWith(rerender, [
            makeRound('r1', 'Looking that up', [
                makeTool({id: 'tc_a', name: 'search_tools'}),
                makeTool({id: 'tc_b', name: 'load_tool'}),
                makeTool({id: 'tc_c', name: 'list_channels'}),
                makeTool({id: 'tc_d', name: 'read_channel'}),
            ]),
            makeRound('r2', 'Almost there', [makeTool({id: 'tc_e', name: 'create_post', status: ToolCallStatus.Pending})]),
        ]);

        advanceAnimation();
        expect(screen.getByText('Create Post')).toBeTruthy();
        expect(screen.queryByText('Load Tool')).toBeNull();
    });
});

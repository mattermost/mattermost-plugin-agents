// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {useSelector} from 'react-redux';

import {ToolApprovalStage, ToolCall, ToolCallStatus} from '../tool_types';

import {deriveActivity} from './activity_items';
import ToolActivityDisplay from './tool_activity_display';
import type {Round} from './turn_content_utils';

jest.mock('react-redux', () => ({
    useSelector: jest.fn(),
}));

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('@/client', () => ({
    doToolCall: jest.fn(),
    doToolResult: jest.fn(),
}));

jest.mock('@/hooks/use_conversation', () => ({
    invalidateConversation: jest.fn(),
}));

jest.mock('../post_text', () => ({
    __esModule: true,
    default: ({message}: {message: string}) => <div>{message}</div>,
}));

const mockUseSelector = useSelector as unknown as jest.Mock;

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

function renderActivity(
    rounds: Round[],
    options: {
        expanded?: boolean;
        inProgress?: boolean;
        approvalStage?: ToolApprovalStage;
        canApprove?: boolean;
        onToggleExpanded?: (expanded: boolean) => void;
    } = {},
) {
    const activity = deriveActivity(rounds);
    return render(
        <IntlProvider locale='en'>
            <ToolActivityDisplay
                activity={activity}
                expanded={options.expanded ?? false}
                onToggleExpanded={options.onToggleExpanded ?? jest.fn()}
                inProgress={options.inProgress ?? false}
                renderRound={(round) => <div key={round.id}>{`round:${round.id}`}</div>}
                postID='post_1'
                conversationID='conv_1'
                canApprove={options.canApprove ?? true}
                canExpand={true}
                approvalStage={options.approvalStage ?? 'done'}
            />
        </IntlProvider>,
    );
}

beforeEach(() => {
    mockUseSelector.mockImplementation((selector) => selector({
        entities: {
            general: {config: {}},
            teams: {currentTeamId: 'team_1'},
        },
    }));

    // @ts-ignore -- ToolCard reads the host webapp's markdown helpers.
    window.PostUtils = {
        formatText: (text: string) => text,
        messageHtmlToComponent: (text: string) => <div>{text}</div>,
    };
});

describe('ToolActivityDisplay collapsed rendering', () => {
    const finishedRounds = [
        makeRound('r1', 'Let me get the jira tools loaded', [
            makeTool({id: 'tc_a', name: 'search_tools'}),
            makeTool({id: 'tc_b', name: 'load_tool'}),
        ]),
        makeRound('r2', 'Now let me create the Jira ticket', [makeTool({id: 'tc_c', name: 'CreateJiraIssue'})]),
    ];

    test('shows only the summary row and hides the stacked rounds by default', () => {
        renderActivity(finishedRounds);

        expect(screen.getByText('Used 3 tools')).toBeTruthy();
        expect(screen.queryByText('round:r1')).toBeNull();
        expect(screen.queryByText('round:r2')).toBeNull();
    });

    test('shows the running tool as the current item while the response is in progress', () => {
        renderActivity(
            [makeRound('r1', 'Looking that up', [makeTool({id: 'tc_a', name: 'search_tools', status: ToolCallStatus.Pending})])],
            {inProgress: true},
        );

        expect(screen.getByText('Search Tools')).toBeTruthy();
        expect(screen.queryByText('Used 1 tool')).toBeNull();
    });

    // A tool can still be running after the stream stops (e.g. a call waiting
    // on approval), so completion is not just "not generating".
    test('does not summarize while a tool call is still pending', () => {
        renderActivity(
            [makeRound('r1', '', [makeTool({id: 'tc_a', name: 'create_post', status: ToolCallStatus.Pending})])],
            {inProgress: false},
        );

        expect(screen.getByText('Create Post')).toBeTruthy();
        expect(screen.queryByText('Used 1 tool')).toBeNull();
    });

    test('expands to the full stack of rounds when the header is clicked', () => {
        const onToggleExpanded = jest.fn();
        const {rerender} = renderActivity(finishedRounds, {onToggleExpanded});

        fireEvent.click(screen.getByTestId('llm-bot-tool-activity-header'));
        expect(onToggleExpanded).toHaveBeenCalledWith(true);

        rerender(
            <IntlProvider locale='en'>
                <ToolActivityDisplay
                    activity={deriveActivity(finishedRounds)}
                    expanded={true}
                    onToggleExpanded={onToggleExpanded}
                    inProgress={false}
                    renderRound={(round) => <div key={round.id}>{`round:${round.id}`}</div>}
                    postID='post_1'
                    conversationID='conv_1'
                    canApprove={true}
                    canExpand={true}
                    approvalStage='done'
                />
            </IntlProvider>,
        );

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

    // Advancing has to happen in two passes: the state update that swaps the
    // row only schedules the timer that retires the outgoing row once React
    // has flushed it.
    function advanceAnimation() {
        act(() => {
            jest.advanceTimersByTime(1000);
        });
        act(() => {
            jest.advanceTimersByTime(1000);
        });
    }

    function rerenderWith(rerender: (ui: React.ReactElement) => void, rounds: Round[]) {
        rerender(
            <IntlProvider locale='en'>
                <ToolActivityDisplay
                    activity={deriveActivity(rounds)}
                    expanded={false}
                    onToggleExpanded={jest.fn()}
                    inProgress={true}
                    renderRound={(round) => <div key={round.id}>{`round:${round.id}`}</div>}
                    postID='post_1'
                    conversationID='conv_1'
                    canApprove={true}
                    canExpand={true}
                    approvalStage='done'
                />
            </IntlProvider>,
        );
    }

    test('steps through newly appended items instead of skipping them', () => {
        const {rerender} = renderActivity(
            [makeRound('r1', 'Looking that up', [makeTool({id: 'tc_a', name: 'search_tools', status: ToolCallStatus.Pending})])],
            {inProgress: true},
        );
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

    // When updates arrive faster than the row can animate, showing a long
    // backlog would misreport what the agent is doing right now.
    test('jumps to the newest item when the backlog outruns the animation', () => {
        const {rerender} = renderActivity(
            [makeRound('r1', 'Looking that up', [makeTool({id: 'tc_a', name: 'search_tools', status: ToolCallStatus.Pending})])],
            {inProgress: true},
        );

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

describe('ToolActivityDisplay approval while collapsed', () => {
    const pendingRounds = [
        makeRound('r1', 'I will post that', [makeTool({id: 'tc_a', name: 'create_post', status: ToolCallStatus.Pending})]),
    ];

    test('renders the approval controls for the requester without expanding', () => {
        renderActivity(pendingRounds, {approvalStage: 'call', canApprove: true});

        expect(screen.getByRole('button', {name: 'Accept'})).toBeTruthy();
        expect(screen.getByRole('button', {name: 'Reject'})).toBeTruthy();
        expect(screen.queryByTestId('llm-bot-tool-activity-rounds')).toBeNull();
    });

    // The approval card already shows the round in full, so repeating the tool
    // name in the row directly above it would just be noise.
    test('does not repeat the awaited tool in the row above the approval card', () => {
        renderActivity(pendingRounds, {approvalStage: 'call', canApprove: true});

        expect(screen.getAllByText('Create Post')).toHaveLength(1);
        expect(screen.queryByTestId('llm-bot-tool-activity-header')).toBeNull();
    });

    // With earlier activity to report, the row keeps summarising that while
    // the pending round is handled by the card below it.
    test('keeps showing earlier activity in the row while a decision is pending', () => {
        renderActivity(
            [
                makeRound('r0', 'Let me look that up', [makeTool({id: 'tc_a', name: 'search_tools'})]),
                ...pendingRounds,
            ],
            {approvalStage: 'call', canApprove: true},
        );

        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Search Tools');
        expect(screen.getAllByText('Create Post')).toHaveLength(1);
        expect(screen.getByRole('button', {name: 'Accept'})).toBeTruthy();
    });

    // Onlookers never decide; for them a pending call is just the current
    // activity item, exactly as if it had been auto-approved.
    test('renders no approval controls for an onlooker', () => {
        renderActivity(pendingRounds, {approvalStage: 'call', canApprove: false});

        expect(screen.getByText('Create Post')).toBeTruthy();
        expect(screen.queryByRole('button', {name: 'Accept'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Reject'})).toBeNull();
    });

    test('renders no approval controls once the stage is done', () => {
        renderActivity(
            [makeRound('r1', 'posted', [makeTool({id: 'tc_a', name: 'create_post', status: ToolCallStatus.Success})])],
            {approvalStage: 'done', canApprove: true},
        );

        expect(screen.queryByRole('button', {name: 'Accept'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Share'})).toBeNull();
    });
});

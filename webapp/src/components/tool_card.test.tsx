// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {useSelector} from 'react-redux';

import ToolCard, {parseAskAnotherUserCanceled, parseAskAnotherUserDecline, parseAskAnotherUserTarget, type AskCancelState} from './tool_card';
import {AskAnotherUserToolName, ToolApprovalStage, ToolCall, ToolCallStatus} from './tool_types';

jest.mock('react-redux', () => ({
    useSelector: jest.fn(),
}));

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

const mockUseSelector = useSelector as unknown as jest.Mock;
const formatTextMock = jest.fn((text: string) => text);
const messageHtmlToComponentMock = jest.fn((text: string) => <div>{text}</div>);

function makeTool(overrides: Partial<ToolCall> = {}): ToolCall {
    return {
        id: 'tool_1',
        name: 'create_jira_issue',
        description: '',
        status: ToolCallStatus.Pending,
        ...overrides,
    };
}

function renderComponent(
    tool: ToolCall,
    decisionProps: {
        onApprove?: () => void;
        onReject?: () => void;
        approvalStage?: ToolApprovalStage;
        isCollapsed?: boolean;
        onCancelAsk?: () => void;
        askCancelState?: AskCancelState;
    } = {},
) {
    return render(
        <IntlProvider locale='en'>
            <ToolCard
                postID='post_1'
                tool={tool}
                isCollapsed={false}
                isProcessing={false}
                onToggleCollapse={jest.fn()}
                canExpand={false}
                showArguments={true}
                showResults={false}
                {...decisionProps}
            />
        </IntlProvider>,
    );
}

beforeEach(() => {
    mockUseSelector.mockImplementation((selector) => selector({
        entities: {
            general: {
                config: {
                    SiteURL: 'http://localhost:8065',
                },
            },
            teams: {
                currentTeamId: 'team_1',
            },
        },
    }));

    formatTextMock.mockClear();
    messageHtmlToComponentMock.mockClear();

    (window as unknown as Window & {
        PostUtils: {
            formatText: typeof formatTextMock;
            messageHtmlToComponent: typeof messageHtmlToComponentMock;
        };
    }).PostUtils = {
        formatText: formatTextMock,
        messageHtmlToComponent: messageHtmlToComponentMock,
    };
});

describe('ToolCard argument rendering', () => {
    test('shows the no-parameters message for explicit empty object arguments', () => {
        renderComponent(makeTool({arguments: {}}));

        expect(screen.getByText(/No parameters required/)).not.toBeNull();
    });

    test('does not show the no-parameters message for hidden arguments', () => {
        renderComponent(makeTool({}));

        expect(screen.queryByText(/No parameters required/)).toBeNull();
        expect(formatTextMock).not.toHaveBeenCalled();
        expect(messageHtmlToComponentMock).not.toHaveBeenCalled();
    });
});

describe('parseAskAnotherUserTarget', () => {
    test.each([
        ['object arguments with a username', {username: 'bob', question: 'x'}, 'bob'],
        ['username with a leading @ is stripped', {username: '@bob'}, 'bob'],
        ['null arguments (redacted for observers)', null, ''],
        ['array arguments', [{username: 'bob'}], ''],
        ['non-string username', {username: 42}, ''],
    ] as const)('%s', (_label, args, expected) => {
        expect(parseAskAnotherUserTarget(args as ToolCall['arguments'])).toBe(expected);
    });
});

describe('parseAskAnotherUserDecline', () => {
    test.each<[string, Partial<ToolCall>, string | null]>([
        [
            'decline result with a target_username',
            {name: AskAnotherUserToolName, result: '{"status":"declined","target_username":"bob"}'},
            'bob',
        ],
        [
            'decline result without target_username falls back to the arguments',
            {name: AskAnotherUserToolName, result: '{"status":"declined"}', arguments: {username: 'bob'}},
            'bob',
        ],
        [
            'decline result with neither username source',
            {name: AskAnotherUserToolName, result: '{"status":"declined"}'},
            '',
        ],
        [
            'answered result',
            {name: AskAnotherUserToolName, result: '{"status":"answered","target_username":"bob"}'},
            null,
        ],
        [
            'other tool with a decline-shaped result',
            {name: 'other_tool', result: '{"status":"declined","target_username":"bob"}'},
            null,
        ],
        [
            'non-JSON result',
            {name: AskAnotherUserToolName, result: 'Tool call rejected by user'},
            null,
        ],
        [
            'no result',
            {name: AskAnotherUserToolName},
            null,
        ],
    ])('%s', (_label, overrides, expected) => {
        expect(parseAskAnotherUserDecline(makeTool(overrides))).toBe(expected);
    });
});

describe('ToolCard waiting state', () => {
    test('shows the named waiting row without decision buttons', () => {
        const {container} = renderComponent(
            makeTool({
                name: AskAnotherUserToolName,
                status: ToolCallStatus.Waiting,
                arguments: {username: 'bob', question: 'q'},
            }),
            {
                approvalStage: 'done',
                onApprove: jest.fn(),
                onReject: jest.fn(),
            },
        );

        expect(screen.getByText('Waiting for @bob to answer…')).not.toBeNull();
        expect(container.querySelector('svg')).not.toBeNull();
        expect(screen.queryByRole('button', {name: 'Accept'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Reject'})).toBeNull();
    });

    test('falls back to the generic waiting row when arguments are redacted', () => {
        renderComponent(
            makeTool({name: AskAnotherUserToolName, status: ToolCallStatus.Waiting}),
            {approvalStage: 'done'},
        );

        expect(screen.getByText('Waiting for a response…')).not.toBeNull();
    });

    test('still shows the waiting row when the card is collapsed', () => {
        renderComponent(
            makeTool({
                name: AskAnotherUserToolName,
                status: ToolCallStatus.Waiting,
                arguments: {username: 'bob', question: 'q'},
            }),
            {approvalStage: 'done', isCollapsed: true},
        );

        expect(screen.getByText('Waiting for @bob to answer…')).not.toBeNull();
    });
});

describe('parseAskAnotherUserCanceled', () => {
    test.each<[string, Partial<ToolCall>, boolean]>([
        [
            'canceled result JSON',
            {name: AskAnotherUserToolName, result: '{"status":"canceled","target_username":"bob"}'},
            true,
        ],
        [
            'declined result JSON',
            {name: AskAnotherUserToolName, result: '{"status":"declined","target_username":"bob"}'},
            false,
        ],
        [
            'non-JSON result',
            {name: AskAnotherUserToolName, result: 'not json'},
            false,
        ],
        [
            'missing result',
            {name: AskAnotherUserToolName},
            false,
        ],
        [
            'canceled JSON on a different tool',
            {name: 'other_tool', result: '{"status":"canceled"}'},
            false,
        ],
    ])('%s', (_label, overrides, expected) => {
        expect(parseAskAnotherUserCanceled(makeTool(overrides))).toBe(expected);
    });
});

describe('ToolCard cancel control', () => {
    const waitingAskTool = () => makeTool({
        name: AskAnotherUserToolName,
        status: ToolCallStatus.Waiting,
        arguments: {username: 'bob', question: 'q'},
    });

    test('renders the cancel button while idle and forwards the click', () => {
        const onCancelAsk = jest.fn();
        renderComponent(waitingAskTool(), {approvalStage: 'done', onCancelAsk, askCancelState: 'idle'});

        const button = screen.getByRole('button', {name: 'Cancel question'});
        fireEvent.click(button);

        expect(onCancelAsk).toHaveBeenCalledTimes(1);
    });

    test('renders no cancel control without an onCancelAsk handler (observer)', () => {
        renderComponent(waitingAskTool(), {approvalStage: 'done'});

        expect(screen.queryByRole('button', {name: 'Cancel question'})).toBeNull();
        expect(screen.getByText('Waiting for @bob to answer…')).not.toBeNull();
    });

    test('replaces the button with the canceling text while submitting', () => {
        renderComponent(waitingAskTool(), {approvalStage: 'done', onCancelAsk: jest.fn(), askCancelState: 'submitting'});

        expect(screen.queryByRole('button', {name: 'Cancel question'})).toBeNull();
        expect(screen.getByText('Canceling…')).not.toBeNull();
    });

    test('shows the failure line and keeps the button on error', () => {
        renderComponent(waitingAskTool(), {approvalStage: 'done', onCancelAsk: jest.fn(), askCancelState: 'error'});

        expect(screen.getByText('Failed to cancel the question. Please try again.')).not.toBeNull();
        expect(screen.getByRole('button', {name: 'Cancel question'})).not.toBeNull();
    });
});

describe('ToolCard canceled terminal rendering', () => {
    test('renders the canceled line for an expanded canceled AskAnotherUser call', () => {
        renderComponent(makeTool({
            name: AskAnotherUserToolName,
            status: ToolCallStatus.Success,
            result: '{"status":"canceled","target_username":"bob"}',
        }));

        expect(screen.getByText('Canceled — the agent continued without an answer')).not.toBeNull();
    });

    test('renders no canceled line for an ordinary successful tool', () => {
        renderComponent(makeTool({
            status: ToolCallStatus.Success,
            result: '{"ok":true}',
        }));

        expect(screen.queryByText(/Canceled — the agent continued/)).toBeNull();
    });
});

describe('ToolCard declined rendering', () => {
    test('renders who declined an AskAnotherUser call instead of Rejected', () => {
        renderComponent(makeTool({
            name: AskAnotherUserToolName,
            status: ToolCallStatus.Rejected,
            result: '{"status":"declined","target_username":"bob"}',
        }));

        expect(screen.getByText('@bob declined to answer')).not.toBeNull();
        expect(screen.queryByText('Rejected')).toBeNull();
    });

    test('renders the unknown-decliner message when no username is available', () => {
        // Redacted/empty arguments and a result without target_username: the
        // decline is known but the decliner is not.
        renderComponent(makeTool({
            name: AskAnotherUserToolName,
            status: ToolCallStatus.Rejected,
            result: '{"status":"declined"}',
        }));

        expect(screen.getByText('Declined to answer')).not.toBeNull();
        expect(screen.queryByText('Rejected')).toBeNull();
    });

    test('an ordinary rejected tool still renders Rejected', () => {
        renderComponent(makeTool({status: ToolCallStatus.Rejected}));

        expect(screen.getByText('Rejected')).not.toBeNull();
    });
});

describe('ToolCard pending state', () => {
    test('shows a spinner without buttons for a live auto-executing tool', () => {
        const {container} = renderComponent(
            makeTool({would_auto_execute: true}),
            {approvalStage: 'done'},
        );

        expect(container.querySelector('svg')).not.toBeNull();
        expect(screen.queryByRole('button', {name: 'Accept'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Reject'})).toBeNull();
    });

    test('never shows accept or reject for a policy-approved pending tool', () => {
        renderComponent(
            makeTool({would_auto_execute: true}),
            {
                approvalStage: 'call',
                onApprove: jest.fn(),
                onReject: jest.fn(),
            },
        );

        expect(screen.queryByRole('button', {name: 'Accept'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Reject'})).toBeNull();
    });
});

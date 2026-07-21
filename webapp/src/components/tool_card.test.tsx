// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {useSelector} from 'react-redux';

import ToolCard from './tool_card';
import {ToolApprovalStage, ToolCall, ToolCallStatus} from './tool_types';

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

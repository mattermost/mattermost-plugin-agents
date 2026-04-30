// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {useSelector} from 'react-redux';

import ToolCard from './tool_card';
import {ToolCall, ToolCallStatus} from './tool_types';

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

function renderComponent(tool: ToolCall) {
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

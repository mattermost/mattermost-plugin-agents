// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';

import type {LLMBot} from '@/bots';

import RHSHeader from './rhs_header';

jest.mock('@/client', () => ({
    disconnectMCPOAuth: jest.fn(),
    getUserMCPTools: jest.fn(),
    refreshUserMCPTools: jest.fn(),
    updateUserToolPreferences: jest.fn(),
}));

// OverlayTrigger is provided by the host Mattermost webapp, not this package.
jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('@/hooks/use_mcp_connection_events', () => ({
    useMCPConnectionEvents: jest.fn(),
}));

jest.mock('react-intl', () => ({
    FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    useIntl: () => ({
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    }),
}));

jest.mock('@mattermost/compass-icons/components', () => ({
    ChevronDownIcon: () => <span data-testid='chevron-icon'/>,
    RefreshIcon: () => <span data-testid='refresh-icon'/>,
}));

jest.mock('../bot_selector', () => ({
    BotDropdown: () => <div data-testid='bot-dropdown'/>,
}));

// Cast instead of importing the enums; system_console/bot.tsx drags in the whole console tree.
const activeBot = {
    id: 'bot-id',
    displayName: 'Agents',
    username: 'ai',
    lastIconUpdate: 0,
    dmChannelID: 'dm-channel-id',
    channelAccessLevel: 0,
    channelIDs: [],
    userAccessLevel: 0,
    userIDs: [],
    enabledMCPTools: [],
    autoEnableNewMCPTools: true,
} as LLMBot;

function renderHeader(bot: LLMBot) {
    return render(
        <RHSHeader
            currentTab='new'
            bots={null}
            activeBot={bot}
            setCurrentTab={jest.fn()}
            selectPost={jest.fn()}
            setActiveBot={jest.fn()}
            disabledServers={[]}
            onDisabledServersChange={jest.fn()}
        />,
    );
}

describe('RHSHeader', () => {
    test('shows the Tools popover for a regular agent', () => {
        renderHeader(activeBot);

        expect(screen.getByRole('button', {name: 'Tools'})).not.toBeNull();
    });

    test('hides the Tools popover when the active agent uses service account auth', () => {
        renderHeader({...activeBot, useServiceAccountAuth: true});

        expect(screen.queryByRole('button', {name: 'Tools'})).toBeNull();
    });
});

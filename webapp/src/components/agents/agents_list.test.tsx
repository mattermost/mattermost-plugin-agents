// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, waitFor} from '@testing-library/react';
import {useSelector} from 'react-redux';

import {getAgents, getServices} from '@/client';
import {useIsMultiLLMLicensed} from '@/license';
import {userHasSystemPermission} from '@/utils/permissions';
import {UserAgent} from '@/types/agents';

import AgentsList from './agents_list';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

    // Stable intl object so effects depending on `intl` don't refire every render.
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('react-redux', () => ({
    useSelector: jest.fn(),
}));

// OverlayTrigger renders the overlay alongside children so tests can assert the tooltip text.
jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children, overlay}: {children: React.ReactNode; overlay: React.ReactNode}) => <>{children}{overlay}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('@/license', () => ({
    useIsMultiLLMLicensed: jest.fn(),
}));

jest.mock('@/client', () => ({
    getAgents: jest.fn(),
    getServices: jest.fn(),
    deleteAgent: jest.fn(),
}));

jest.mock('@/utils/permissions', () => ({
    userHasSystemPermission: jest.fn(),
}));

jest.mock('./agent_row', () => ({
    __esModule: true,
    default: ({agent}: {agent: UserAgent}) => <div data-testid='agent-row'>{agent.displayName}</div>,
}));

jest.mock('./agent_config_view', () => ({
    __esModule: true,
    default: () => null,
}));

jest.mock('./delete_agent_dialog', () => ({
    __esModule: true,
    default: () => null,
}));

const mockUseSelector = useSelector as unknown as jest.Mock;
const mockUseIsMultiLLMLicensed = useIsMultiLLMLicensed as unknown as jest.Mock;
const mockGetAgents = getAgents as unknown as jest.Mock;
const mockGetServices = getServices as unknown as jest.Mock;
const mockUserHasSystemPermission = userHasSystemPermission as unknown as jest.Mock;

const tooltipText = 'Multiple self-service agents require a qualifying Mattermost plan';

function makeAgent(id: string): UserAgent {
    return {
        id,
        name: id,
        displayName: `Agent ${id}`,
        creatorID: 'user_1',
    } as UserAgent;
}

function renderList() {
    return render(<AgentsList/>);
}

beforeEach(() => {
    jest.clearAllMocks();
    mockUseSelector.mockImplementation((selector) => selector({
        entities: {users: {currentUserId: 'user_1'}},
    }));

    // manage_own_agent grants create permission.
    mockUserHasSystemPermission.mockImplementation((_state, _userId, permission) => permission === 'manage_own_agent');
    mockGetServices.mockResolvedValue([]);
});

describe('AgentsList create-button gating', () => {
    test('Pro license with no agents enables Create button without tooltip', async () => {
        mockUseIsMultiLLMLicensed.mockReturnValue(false);
        mockGetAgents.mockResolvedValue({agents: [], activeAgentCount: 0});

        renderList();

        const button = await screen.findByRole('button', {name: 'Create agent'});
        expect((button as HTMLButtonElement).disabled).toBe(false);
        await waitFor(() => expect(screen.queryByText(tooltipText)).toBeNull());
    });

    test('Pro license at the free-tier limit disables Create button and shows tooltip', async () => {
        mockUseIsMultiLLMLicensed.mockReturnValue(false);
        mockGetAgents.mockResolvedValue({agents: [makeAgent('a1')], activeAgentCount: 1});

        renderList();

        await screen.findByText('Agent a1');
        const button = screen.getByRole('button', {name: 'Create agent'});
        expect((button as HTMLButtonElement).disabled).toBe(true);
        expect(screen.getByText(tooltipText)).not.toBeNull();
    });

    test('Pro license disables Create when server quota is reached but list is empty', async () => {
        mockUseIsMultiLLMLicensed.mockReturnValue(false);
        mockGetAgents.mockResolvedValue({agents: [], activeAgentCount: 1});

        renderList();

        await screen.findByText('Loading agents...').then(() => screen.findByText('No agents have been created yet.'));
        const button = screen.getByRole('button', {name: 'Create agent'});
        expect((button as HTMLButtonElement).disabled).toBe(true);
        expect(screen.getByText(tooltipText)).not.toBeNull();
    });

    test('Create button stays disabled while agents are loading', () => {
        mockUseIsMultiLLMLicensed.mockReturnValue(false);
        mockGetAgents.mockImplementation(() => new Promise(() => {
            // Never resolves: keep the component in its loading state.
        }));

        renderList();

        const button = screen.getByRole('button', {name: 'Create agent'});
        expect((button as HTMLButtonElement).disabled).toBe(true);
    });

    test('Enterprise license keeps Create button enabled regardless of agent count', async () => {
        mockUseIsMultiLLMLicensed.mockReturnValue(true);
        mockGetAgents.mockResolvedValue({agents: [makeAgent('a1'), makeAgent('a2')]});

        renderList();

        await screen.findByText('Agent a1');
        const button = screen.getByRole('button', {name: 'Create agent'});
        expect((button as HTMLButtonElement).disabled).toBe(false);
        expect(screen.queryByText(tooltipText)).toBeNull();
    });
});

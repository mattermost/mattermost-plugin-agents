// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, waitFor, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getAgentScopedAccess} from '@/client';
import type {ScopedAccessReport} from '@/types/agents';

import ScopedAccessView from './scoped_access_view';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/client', () => ({
    getAgentScopedAccess: jest.fn(),
}));

const mockedGetAccess = getAgentScopedAccess as unknown as jest.Mock;

function report(overrides: Partial<ScopedAccessReport> = {}): ScopedAccessReport {
    return {
        configured: true,
        active: true,
        blockers: [],
        channels: {
            triggeringTeamPublicChannels: true,
            openTeams: {
                enabled: true,
                openCount: 1,
                closedCount: 2,
                open: [{id: 't1', displayName: 'Town Square Team'}],
                closed: [
                    {id: 't2', displayName: 'Secret Team', reasons: ['invite_only']},
                    {id: 't3', displayName: 'Synced Team', reasons: ['group_constrained', 'domain_restricted']},
                ],
            },
            extraChannels: [
                {id: 'c1', displayName: 'Runbooks', teamName: 'Ops', status: 'readable'},
                {id: 'c2', status: 'missing'},
                {id: 'c3', displayName: 'Old news', status: 'archived'},
            ],
        },
        tools: [
            {serverOrigin: 'embedded://mattermost', serverName: 'Mattermost', serverKind: 'embedded', name: 'read_channel', offered: true},
            {serverOrigin: 'embedded://mattermost', serverName: 'Mattermost', serverKind: 'embedded', name: 'create_post', offered: false, reason: 'write_tool'},
            {serverOrigin: 'https://wiki.example.com', serverName: 'Wiki', serverKind: 'remote', name: '*', offered: false, reason: 'not_named'},
            {serverOrigin: 'builtin://mattermost-agents', serverKind: 'builtin', name: 'WebSearch', offered: false, reason: 'not_opted_in'},
        ],
        ...overrides,
    };
}

function renderView(hasUnsavedChanges = false) {
    return render(
        <IntlProvider locale='en'>
            <ScopedAccessView
                agentId='agent-1'
                hasUnsavedChanges={hasUnsavedChanges}
            />
        </IntlProvider>,
    );
}

describe('ScopedAccessView', () => {
    beforeEach(() => {
        mockedGetAccess.mockReset();
    });

    test('shows the boundary, channels and tools of the saved agent', async () => {
        mockedGetAccess.mockResolvedValue(report());
        renderView();

        const summary = await screen.findByTestId('scoped-access-summary');
        expect(summary.textContent).toMatch(/only read/);
        expect(summary.textContent).toMatch(/never reads direct messages or private channels outside/);
        expect(mockedGetAccess).toHaveBeenCalledWith('agent-1');

        const channels = screen.getByTestId('scoped-access-channels');
        expect(within(channels).getByText('The channel it is used in')).not.toBeNull();
        expect(within(channels).getByText("Public channels on that channel's team")).not.toBeNull();
        expect(within(channels).getByText('Public channels on open teams')).not.toBeNull();

        const closed = screen.getByTestId('scoped-access-closed-teams');
        expect(within(closed).getByText('Secret Team')).not.toBeNull();
        expect(closed.textContent).toContain('Secret Team (invite-only)');
        expect(closed.textContent).toContain('Synced Team (limited to synced groups, limited to some email domains)');

        const extras = screen.getAllByTestId('scoped-access-extra-channel');
        expect(extras.map((el) => el.textContent)).toEqual([
            'Runbooks Ops Readable',
            'c2 Not found',
            'Old news Archived',
        ]);

        const offered = screen.getByTestId('scoped-access-offered-tools');
        expect(offered.textContent).toBe('read_channel Mattermost');

        const removed = screen.getByTestId('scoped-access-removed-tools');
        expect(removed.textContent).toContain('create_post');
        expect(removed.textContent).toContain('changes data in Mattermost, and this agent can only read');
        expect(removed.textContent).toContain('All tools');
        expect(removed.textContent).toContain('granted as all tools on a server; pick each tool by name');
        expect(removed.textContent).toContain('Built-in tools');
        expect(removed.textContent).toContain('pick it under Built-in tools to allow it');
    });

    test.each([
        {name: 'compliance mode', reason: 'compliance_mode' as const, text: /Compliance mode is on/},
        {name: 'users who cannot join teams', reason: 'users_cannot_join_teams' as const, text: /People can't join teams on their own/},
    ])('explains the open-team grant being off for $name', async ({reason, text}) => {
        mockedGetAccess.mockResolvedValue(report({
            channels: {
                ...report().channels,
                openTeams: {enabled: false, disabledReason: reason, openCount: 0, closedCount: 3, open: [], closed: []},
            },
        }));
        renderView();

        const channels = await screen.findByTestId('scoped-access-channels');
        expect(channels.textContent).toMatch(text);
        expect(screen.queryByTestId('scoped-access-closed-teams')).toBeNull();
    });

    test('lists the unmet prerequisites when the mode cannot run', async () => {
        mockedGetAccess.mockResolvedValue(report({active: false, blockers: ['server_unsupported', 'channel_tool_calling_off']}));
        renderView();

        expect(await screen.findByText("This agent isn't running tools in channels without asking yet.")).not.toBeNull();
        expect(screen.getByText("This Mattermost server can't create scoped sessions.")).not.toBeNull();
        expect(screen.getByText('Tool calling in channels is turned off in the plugin settings.')).not.toBeNull();
    });

    test.each([
        {name: 'unsaved edits are flagged', unsaved: true, shown: true},
        {name: 'a saved form is not', unsaved: false, shown: false},
    ])('$name', async ({unsaved, shown}) => {
        mockedGetAccess.mockResolvedValue(report());
        renderView(unsaved);

        await screen.findByTestId('scoped-access-summary');
        expect(screen.queryByText('This shows the last saved settings. Save your changes to update it.') !== null).toBe(shown);
    });

    test('asks to save an agent whose mode is not saved yet', async () => {
        mockedGetAccess.mockResolvedValue(report({configured: false, active: false}));
        renderView();

        expect(await screen.findByText('Save the agent to see what it can access in channels.')).not.toBeNull();
        expect(screen.queryByTestId('scoped-access-summary')).toBeNull();
    });

    test('says so when the report cannot be loaded', async () => {
        mockedGetAccess.mockRejectedValue(new Error('boom'));
        renderView();

        expect(await screen.findByText("Couldn't load what this agent can access.")).not.toBeNull();
    });

    test('ignores a response that arrives after the agent changed', async () => {
        let resolveFirst: ((value: ScopedAccessReport) => void) | undefined;
        mockedGetAccess.mockReturnValueOnce(new Promise<ScopedAccessReport>((resolve) => {
            resolveFirst = resolve;
        }));
        mockedGetAccess.mockResolvedValueOnce(report({tools: []}));

        const view = renderView();
        view.rerender(
            <IntlProvider locale='en'>
                <ScopedAccessView
                    agentId='agent-2'
                    hasUnsavedChanges={false}
                />
            </IntlProvider>,
        );
        await screen.findByText('None of the tools picked for this agent can be used in channels.');

        resolveFirst?.(report());
        await waitFor(() => expect(mockedGetAccess).toHaveBeenCalledTimes(2));
        expect(screen.getByText('None of the tools picked for this agent can be used in channels.')).not.toBeNull();
    });
});

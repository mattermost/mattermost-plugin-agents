// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {serverSupportsScopedSessions, useServerSupportsScopedSessions} from '@/hooks/use_server_supports_scoped_sessions';

import ScopedChannelModeSection from './scoped_channel_mode_section';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/hooks/use_server_supports_scoped_sessions', () => ({
    ...jest.requireActual('@/hooks/use_server_supports_scoped_sessions'),
    useServerSupportsScopedSessions: jest.fn(() => true),
}));

jest.mock('@/components/select', () => ({
    SelectChannel: ({channelIDs, disabled}: {channelIDs: string[]; disabled?: boolean}) => (
        <div
            data-testid='channel-picker'
            data-disabled={String(Boolean(disabled))}
        >
            {channelIDs.join(',')}
        </div>
    ),
}));

const mockedServerSupport = useServerSupportsScopedSessions as unknown as jest.Mock;

const toggleName = /^Run tools in channels without asking/;
const unsupportedNotice = /needs Mattermost Server 12.2 or later/;
const allowlistNotice = /turn off "Automatically enable all MCP tools"/;
const summary = /^Without asking, this agent can read the channel it's used in/;
const extrasWarning = /^Content from these channels may appear in any channel/;

type Opts = {
    scopedChannelMode?: boolean;
    scopedExtraChannelIds?: string[];
    autoEnableNewMCPTools?: boolean;
    editable?: boolean;
};

function renderSection(opts: Opts = {}) {
    const onChange = jest.fn();
    render(
        <IntlProvider locale='en'>
            <ScopedChannelModeSection
                scopedChannelMode={opts.scopedChannelMode ?? false}
                scopedExtraChannelIds={opts.scopedExtraChannelIds ?? []}
                autoEnableNewMCPTools={opts.autoEnableNewMCPTools ?? false}
                editable={opts.editable ?? true}
                onChange={onChange}
            />
        </IntlProvider>,
    );
    return {onChange, toggle: screen.getByRole('checkbox', {name: toggleName}) as HTMLInputElement};
}

describe('ScopedChannelModeSection', () => {
    beforeEach(() => {
        mockedServerSupport.mockReturnValue(true);
    });

    test('an admin can turn it on', () => {
        const {onChange, toggle} = renderSection();

        expect(toggle.disabled).toBe(false);
        fireEvent.click(toggle);
        expect(onChange).toHaveBeenCalledWith({scopedChannelMode: true});
        expect(screen.queryByText(summary)).toBeNull();
    });

    test.each([
        {name: 'agent managers', opts: {editable: false}, notice: null},
        {name: 'older servers', server: false, opts: {}, notice: unsupportedNotice},
        {name: 'agents that enable every MCP tool', opts: {autoEnableNewMCPTools: true}, notice: allowlistNotice},
    ])('$name cannot turn it on', ({server, opts, notice}) => {
        mockedServerSupport.mockReturnValue(server ?? true);
        const {toggle} = renderSection(opts);

        expect(toggle.disabled).toBe(true);
        if (notice) {
            expect(screen.getByText(notice)).not.toBeNull();
        }
    });

    test('summarizes what the agent can read and warns about extra channels', () => {
        renderSection({scopedChannelMode: true, scopedExtraChannelIds: ['channel-a']});

        expect(screen.getByText(summary)).not.toBeNull();
        expect(screen.getByTestId('channel-picker').textContent).toBe('channel-a');
        expect(screen.getByText(extrasWarning)).not.toBeNull();
    });

    test('an agent manager can see the setting but not change it', () => {
        const {toggle} = renderSection({scopedChannelMode: true, scopedExtraChannelIds: ['channel-a'], editable: false});

        expect(toggle.disabled).toBe(true);
        expect(screen.getByTestId('channel-picker').getAttribute('data-disabled')).toBe('true');
    });

    test('no extra channels means no extra-channel warning', () => {
        renderSection({scopedChannelMode: true});

        expect(screen.queryByText(extrasWarning)).toBeNull();
    });
});

describe('serverSupportsScopedSessions', () => {
    test.each([
        ['12.2.0', true],
        ['12.2.1', true],
        ['12.10.0', true],
        ['13.0.0', true],
        ['12.1.9', false],
        ['11.9.0', false],
        ['', false],
        ['dev', false],
    ])('%s => %s', (version, expected) => {
        expect(serverSupportsScopedSessions(version)).toBe(expected);
    });
});

// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {useServerSupportsScopedSessions} from '@/hooks/use_server_supports_scoped_sessions';

import ScopedChannelModeSection from './scoped_channel_mode_section';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/hooks/use_server_supports_scoped_sessions', () => ({
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
const unsupportedNotice = /^This setting needs a Mattermost server version that supports scoped sessions/;
const allowlistNotice = /turn off "Automatically enable all MCP tools"/;
const allToolsNotice = /^To use this setting, replace the servers granted with all of their tools/;
const allToolsWarning = /^Pick each tool by name/;
const summary = /^Without asking, this agent can read the channel it's used in/;
const extrasWarning = /^Content from these channels may appear in any channel/;

type Opts = {
    scopedChannelMode?: boolean;
    scopedExtraChannelIds?: string[];
    autoEnableNewMCPTools?: boolean;
    hasAllToolsGrants?: boolean;
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
                hasAllToolsGrants={opts.hasAllToolsGrants ?? false}
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
        {name: 'agent managers', server: true, opts: {editable: false}, notice: null},
        {name: 'servers that cannot create scoped sessions', server: false, opts: {}, notice: unsupportedNotice},
        {name: 'admins before the server support is known', server: null, opts: {}, notice: null},
        {name: 'agents that enable every MCP tool', server: true, opts: {autoEnableNewMCPTools: true}, notice: allowlistNotice},
        {name: 'agents granted all tools of a server', server: true, opts: {hasAllToolsGrants: true}, notice: allToolsNotice},
    ])('$name cannot turn it on', ({server, opts, notice}) => {
        mockedServerSupport.mockReturnValue(server);
        const {toggle} = renderSection(opts);

        expect(toggle.disabled).toBe(true);
        if (notice) {
            expect(screen.getByText(notice)).not.toBeNull();
        }
        if (notice !== unsupportedNotice) {
            expect(screen.queryByText(unsupportedNotice)).toBeNull();
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

    test('warns a scoped agent that still has all-tools grants', () => {
        const {toggle} = renderSection({scopedChannelMode: true, hasAllToolsGrants: true});

        expect(toggle.disabled).toBe(false);
        expect(screen.getByText(allToolsWarning)).not.toBeNull();
    });

    test('no extra channels means no extra-channel warning', () => {
        renderSection({scopedChannelMode: true});

        expect(screen.queryByText(extrasWarning)).toBeNull();
    });
});

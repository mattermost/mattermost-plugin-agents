// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import AutoReplyModePicker from './autoreply_mode_picker';
import {setChannelAutoReplyDraft} from './autoreply_state';

jest.mock('@/mm_webapp', () => ({
    AdvancedTextEditor: null,
    CreatePost: null,
    isRHSCompatable: () => false,
    PostMessagePreview: null,
    Timestamp: null,
    ThreadViewer: null,
    DatePicker: null,
    MenuItem: null,
    MenuSeparator: null,
    useWebSocketClient: () => null,
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
}));

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: () => null,
    Tooltip: () => null,
}), {virtual: true});

function renderPicker() {
    const informChange = jest.fn();
    render(<AutoReplyModePicker informChange={informChange}/>);
    return {informChange};
}

describe('AutoReplyModePicker license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
        setChannelAutoReplyDraft({
            channelId: 'chan1',
            saved: {bot_id: 'bot1', mode: 'off'},
            saveError: null,
        });
    });

    afterEach(() => {
        setChannelAutoReplyDraft(null);
    });

    test('shows enabling modes at Enterprise Advanced', () => {
        renderPicker();

        expect(screen.getByText('Off')).not.toBeNull();
        expect(screen.getByText('Top-level posts only')).not.toBeNull();
        expect(screen.getByText('Threads too')).not.toBeNull();
    });

    test('hides enabling modes below Enterprise Advanced when the current mode is off', () => {
        useIsLicensedFor.mockReturnValue(false);
        renderPicker();

        expect(screen.getByText('Off')).not.toBeNull();
        expect(screen.queryByText('Top-level posts only')).toBeNull();
        expect(screen.queryByText('Threads too')).toBeNull();
    });

    test('keeps the current enabling mode visible so it can be switched off', () => {
        useIsLicensedFor.mockReturnValue(false);
        setChannelAutoReplyDraft({
            channelId: 'chan1',
            saved: {bot_id: 'bot1', mode: 'root_posts'},
            saveError: null,
        });
        const {informChange} = renderPicker();

        expect(screen.getByText('Top-level posts only')).not.toBeNull();
        expect(screen.queryByText('Threads too')).toBeNull();

        const radios = screen.getAllByRole('radio') as HTMLInputElement[];
        const off = radios.find((radio) => radio.nextElementSibling?.textContent?.includes('Off'));
        expect(off).toBeDefined();
        fireEvent.click(off as HTMLInputElement);
        expect(informChange).toHaveBeenCalledWith('mode', 'off');
    });
});

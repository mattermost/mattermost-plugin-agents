// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {ChannelSummarizePopover} from './channel_summarize_popover';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        locale: 'en',
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/mm_webapp', () => ({
    DatePicker: null,
}));

jest.mock('./bot_selector', () => ({
    BotDropdown: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
    BotSelectorContainer: 'div',
}));

const bot = {id: 'bot1', username: 'matty', displayName: 'Matty'};
const lastViewedAt = Date.UTC(2026, 0, 2);

function renderPopover() {
    const onSummarize = jest.fn();
    render(
        <IntlProvider locale='en'>
            <ChannelSummarizePopover
                bots={[bot] as any}
                activeBot={bot as any}
                setActiveBot={jest.fn()}
                channelName='Town Square'
                onSummarize={onSummarize}
                lastViewedAt={lastViewedAt}
            />
        </IntlProvider>,
    );
    return {onSummarize};
}

describe('ChannelSummarizePopover', () => {
    it.each([
        {item: 'Summarize unreads', expected: {analysis_type: 'summarize_unreads', since: new Date(lastViewedAt).toISOString()}},
        {item: 'Summarize last 7 days', expected: {analysis_type: 'days', days: 7}},
        {item: 'Summarize last 14 days', expected: {analysis_type: 'days', days: 14}},
    ])('$item requests the matching analysis', ({item, expected}) => {
        const {onSummarize} = renderPopover();

        fireEvent.click(screen.getByRole('button', {name: item}));

        expect(onSummarize).toHaveBeenCalledWith(expected);
    });

    it('focuses the prompt input and submits it on Enter', () => {
        const {onSummarize} = renderPopover();
        const input = screen.getByPlaceholderText('Ask Agents about this channel...');
        expect(document.activeElement).toBe(input);

        fireEvent.keyDown(input, {key: 'Enter'});
        expect(onSummarize).not.toHaveBeenCalled();

        fireEvent.change(input, {target: {value: 'What changed?'}});
        fireEvent.keyDown(input, {key: 'Enter'});

        expect(onSummarize).toHaveBeenCalledWith({analysis_type: 'custom', prompt: 'What changed?'});
    });

    it('enables the send button only once a prompt is typed', () => {
        const {onSummarize} = renderPopover();
        const send = screen.getByRole('button', {name: 'Send'}) as HTMLButtonElement;
        expect(send.disabled).toBe(true);

        fireEvent.change(screen.getByPlaceholderText('Ask Agents about this channel...'), {target: {value: 'Recap'}});
        expect(send.disabled).toBe(false);

        fireEvent.click(send);
        expect(onSummarize).toHaveBeenCalledWith({analysis_type: 'custom', prompt: 'Recap'});
    });

    it('summarizes a chosen date range from the date range dialog', () => {
        const {onSummarize} = renderPopover();

        fireEvent.click(screen.getByRole('button', {name: 'Select date range to summarize'}));

        const dialog = screen.getByRole('dialog', {name: 'Summarize channel'});
        expect(dialog.textContent).toContain('Town Square');

        fireEvent.change(screen.getByLabelText('Start date'), {target: {value: '2026-01-01'}});
        fireEvent.change(screen.getByLabelText('End date'), {target: {value: '2026-01-07'}});
        fireEvent.click(screen.getByRole('button', {name: 'Summarize'}));

        expect(onSummarize).toHaveBeenCalledWith({analysis_type: 'date_range', since: '2026-01-01', until: '2026-01-07'});
    });
});

// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';

import {getChannelInstructions} from '@/client';

import {ChannelInstructionsSetting, MAX_CHANNEL_INSTRUCTIONS_LENGTH} from './channel_instructions_setting';
import {
    ChannelInstructionsSaveErrorKind,
    getChannelInstructionsDraft,
    handleChannelInstructionsUpdated,
    setChannelInstructionsDraft,
} from './channel_instructions_state';

// Message ids are injected by babel-plugin-formatjs at build time; under
// ts-jest FormattedMessage has no id, so render the defaultMessage with any
// plain values substituted.
jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage, values}: {defaultMessage: string; values?: Record<string, unknown>}) => (
            Object.entries(values ?? {}).reduce(
                (text, [key, value]) => (typeof value === 'function' ? text : text.replace(`{${key}}`, String(value))),
                defaultMessage,
            )
        ),
    };
});

jest.mock('@/client', () => ({
    getChannelInstructions: jest.fn(),
}));

const mockedGetChannelInstructions = getChannelInstructions as jest.MockedFunction<typeof getChannelInstructions>;
const CHANNEL_ID = 'chan1';

function seed(saved: string, saveError: ChannelInstructionsSaveErrorKind | null = null) {
    setChannelInstructionsDraft({channelId: CHANNEL_ID, saved, saveError});
}

beforeEach(() => {
    setChannelInstructionsDraft(null);
    mockedGetChannelInstructions.mockReset();
});

describe('ChannelInstructionsSetting', () => {
    test('shows the saved instructions, a counter, and the pinning hint', () => {
        seed('Deploys freeze on Fridays.');
        render(<ChannelInstructionsSetting informChange={jest.fn()}/>);

        const textarea = screen.getByTestId('channel-instructions-textarea') as HTMLTextAreaElement;
        expect(textarea.value).toBe('Deploys freeze on Fridays.');
        expect(textarea.maxLength).toBe(MAX_CHANNEL_INSTRUCTIONS_LENGTH);
        expect(screen.getByText(`26/${MAX_CHANNEL_INSTRUCTIONS_LENGTH}`)).not.toBeNull();
        expect(screen.getByTestId('channel-context-pin-hint').textContent).toContain('Pin to agent context');
    });

    test('typing reports the full text to the host and clears a stale save error', () => {
        seed('Old', 'generic');
        const informChange = jest.fn();
        render(<ChannelInstructionsSetting informChange={informChange}/>);

        fireEvent.change(screen.getByTestId('channel-instructions-textarea'), {target: {value: 'New instructions'}});

        expect(informChange).toHaveBeenCalledWith('instructions', 'New instructions');
        expect(getChannelInstructionsDraft()?.saveError).toBeNull();
    });

    test('never reports a change on mount, so opening the tab does not make it dirty', () => {
        seed('Saved');
        const informChange = jest.fn();
        render(<ChannelInstructionsSetting informChange={informChange}/>);

        expect(informChange).not.toHaveBeenCalled();
    });

    test('a remote change shows through until the user edits, then the local edit wins', async () => {
        seed('Before');
        mockedGetChannelInstructions.mockResolvedValue({instructions: 'Changed elsewhere'});
        render(<ChannelInstructionsSetting informChange={jest.fn()}/>);
        const textarea = screen.getByTestId('channel-instructions-textarea') as HTMLTextAreaElement;

        await act(() => handleChannelInstructionsUpdated({channel_id: CHANNEL_ID}));
        expect(textarea.value).toBe('Changed elsewhere');

        fireEvent.change(textarea, {target: {value: 'Mine'}});
        mockedGetChannelInstructions.mockResolvedValue({instructions: 'Changed again'});
        await act(() => handleChannelInstructionsUpdated({channel_id: CHANNEL_ID}));
        expect(textarea.value).toBe('Mine');
    });

    test('ignores remote changes for another channel', async () => {
        seed('Before');

        await handleChannelInstructionsUpdated({channel_id: 'other'});

        expect(mockedGetChannelInstructions).not.toHaveBeenCalled();
    });

    test.each([
        {kind: 'forbidden' as const, text: 'You don’t have permission to change the instructions for this channel.'},
        {kind: 'invalid' as const, text: `Instructions can be at most ${MAX_CHANNEL_INSTRUCTIONS_LENGTH} characters.`},
        {kind: 'generic' as const, text: 'Failed to save channel instructions. Please try again.'},
    ])('shows the $kind save error', ({kind, text}) => {
        seed('Saved', kind);
        render(<ChannelInstructionsSetting informChange={jest.fn()}/>);

        expect(screen.getByText(text)).not.toBeNull();
    });

    test('shows a load failure when hydration failed', () => {
        render(<ChannelInstructionsSetting informChange={jest.fn()}/>);

        expect(screen.getByText('Channel instructions could not be loaded. Close the dialog and try again.')).not.toBeNull();
        expect(screen.queryByTestId('channel-instructions-textarea')).toBeNull();
    });
});

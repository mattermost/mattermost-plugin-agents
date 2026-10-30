// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';

import {AutoReplyInstructionsField} from './autoreply_ambient_fields';
import {MAX_INSTRUCTIONS_RUNES, getChannelAutoReplyDraft, setChannelAutoReplyDraft} from './autoreply_state';

// mm_webapp reads window.Components/ProductApi at module load, which are absent
// in jsdom. Stub it so importing the bots chain from the draft store doesn't throw.
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

// Message ids are injected by babel-plugin-formatjs at build time; under
// ts-jest FormattedMessage has no id, so use the repo's standard stub that
// renders the defaultMessage.
jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage, values = {}}: {defaultMessage: string; values?: Record<string, unknown>}) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_, key) => String(values[key]))
        ),
    };
});

jest.mock('@/client', () => ({
    getChannelAutoReply: jest.fn(),
}));

const CHANNEL_ID = 'chan1';

function seedDraft(overrides: {instructions?: string} = {}) {
    setChannelAutoReplyDraft({
        channelId: CHANNEL_ID,
        saved: {
            bot_id: 'alpha',
            mode: 'ambient',
            instructions: overrides.instructions ?? 'saved instructions',
        },
        saveError: null,
    });
}

beforeEach(() => {
    setChannelAutoReplyDraft(null);
});

describe('AutoReplyInstructionsField', () => {
    test('shows the draft-saved instructions on mount without calling informChange', () => {
        seedDraft();
        const informChange = jest.fn();

        render(<AutoReplyInstructionsField informChange={informChange}/>);

        expect((screen.getByTestId('autoreply-instructions') as HTMLTextAreaElement).value).toBe('saved instructions');
        expect(informChange).not.toHaveBeenCalled();
    });

    test('associates the textarea with its label', () => {
        seedDraft();

        render(<AutoReplyInstructionsField informChange={jest.fn()}/>);

        expect(screen.getByLabelText('Ambient instructions')).toBe(screen.getByTestId('autoreply-instructions'));
    });

    test('typing reports the value via informChange and updates the display', () => {
        seedDraft();
        const informChange = jest.fn();
        render(<AutoReplyInstructionsField informChange={informChange}/>);

        fireEvent.change(screen.getByTestId('autoreply-instructions'), {target: {value: 'only if asked'}});

        expect(informChange).toHaveBeenCalledTimes(1);
        expect(informChange).toHaveBeenCalledWith('instructions', 'only if asked');
        expect((screen.getByTestId('autoreply-instructions') as HTMLTextAreaElement).value).toBe('only if asked');
    });

    test('a remote draft update refreshes the display when the user has not edited locally', () => {
        seedDraft();
        render(<AutoReplyInstructionsField informChange={jest.fn()}/>);

        act(() => {
            seedDraft({instructions: 'remote instructions'});
        });

        expect((screen.getByTestId('autoreply-instructions') as HTMLTextAreaElement).value).toBe('remote instructions');
    });

    test('a remote draft update does not override a local edit', () => {
        seedDraft();
        render(<AutoReplyInstructionsField informChange={jest.fn()}/>);

        fireEvent.change(screen.getByTestId('autoreply-instructions'), {target: {value: 'local instructions'}});

        act(() => {
            seedDraft({instructions: 'remote instructions'});
        });

        expect((screen.getByTestId('autoreply-instructions') as HTMLTextAreaElement).value).toBe('local instructions');
    });

    test('renders nothing when no draft is hydrated', () => {
        const informChange = jest.fn();

        const {container} = render(<AutoReplyInstructionsField informChange={informChange}/>);

        expect(container.firstChild).toBeNull();
        expect(screen.queryByTestId('autoreply-instructions')).toBeNull();
        expect(informChange).not.toHaveBeenCalled();
    });

    test('clears a displayed save error when the user edits', () => {
        setChannelAutoReplyDraft({
            channelId: CHANNEL_ID,
            saved: {bot_id: 'alpha', mode: 'ambient', instructions: ''},
            saveError: 'invalid',
        });
        render(<AutoReplyInstructionsField informChange={jest.fn()}/>);

        fireEvent.change(screen.getByTestId('autoreply-instructions'), {target: {value: 'x'}});

        expect(getChannelAutoReplyDraft()?.saveError).toBeNull();
    });

    describe('length limit', () => {
        const tooLongMessage = `Instructions are too long. Shorten them to ${MAX_INSTRUCTIONS_RUNES} characters or fewer to save.`;

        test.each([
            {name: 'the exact limit of ASCII characters', value: 'a'.repeat(MAX_INSTRUCTIONS_RUNES), over: false, count: MAX_INSTRUCTIONS_RUNES},
            {name: 'one ASCII character over the limit', value: 'a'.repeat(MAX_INSTRUCTIONS_RUNES + 1), over: true, count: MAX_INSTRUCTIONS_RUNES + 1},
            {name: 'astral characters at the limit (counted as runes, not UTF-16 units)', value: '😀'.repeat(MAX_INSTRUCTIONS_RUNES), over: false, count: MAX_INSTRUCTIONS_RUNES},
            {name: 'astral characters one over the limit', value: '😀'.repeat(MAX_INSTRUCTIONS_RUNES + 1), over: true, count: MAX_INSTRUCTIONS_RUNES + 1},
        ])('with $name', ({value, over, count}) => {
            seedDraft({instructions: value});

            render(<AutoReplyInstructionsField informChange={jest.fn()}/>);

            expect(screen.getByTestId('autoreply-instructions-counter').textContent).toBe(`${count} / ${MAX_INSTRUCTIONS_RUNES}`);
            expect(screen.queryByText(tooLongMessage) !== null).toBe(over);
            expect(screen.getByTestId('autoreply-instructions').getAttribute('aria-invalid')).toBe(String(over));
        });

        test('typing past the limit shows the error and still reports the value', () => {
            seedDraft();
            const informChange = jest.fn();
            render(<AutoReplyInstructionsField informChange={informChange}/>);
            const value = 'a'.repeat(MAX_INSTRUCTIONS_RUNES + 1);

            fireEvent.change(screen.getByTestId('autoreply-instructions'), {target: {value}});

            expect(screen.getByText(tooLongMessage)).not.toBeNull();
            expect(informChange).toHaveBeenCalledWith('instructions', value);
        });
    });
});

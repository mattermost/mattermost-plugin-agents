// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';

import {AutoReplyAnalysisModelField, AutoReplyInstructionsField} from './autoreply_ambient_fields';
import {setChannelAutoReplyDraft} from './autoreply_state';

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
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/client', () => ({
    getChannelAutoReply: jest.fn(),
}));

const CHANNEL_ID = 'chan1';

function seedDraft(overrides: {instructions?: string; analysis_model?: string} = {}) {
    setChannelAutoReplyDraft({
        channelId: CHANNEL_ID,
        saved: {
            bot_id: 'alpha',
            mode: 'ambient',
            instructions: overrides.instructions ?? 'saved instructions',
            analysis_model: overrides.analysis_model ?? 'saved-model',
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
            seedDraft({instructions: 'remote instructions', analysis_model: 'saved-model'});
        });

        expect((screen.getByTestId('autoreply-instructions') as HTMLTextAreaElement).value).toBe('remote instructions');
    });

    test('a remote draft update does not override a local edit', () => {
        seedDraft();
        render(<AutoReplyInstructionsField informChange={jest.fn()}/>);

        fireEvent.change(screen.getByTestId('autoreply-instructions'), {target: {value: 'local instructions'}});

        act(() => {
            seedDraft({instructions: 'remote instructions', analysis_model: 'saved-model'});
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
});

describe('AutoReplyAnalysisModelField', () => {
    test('shows the draft-saved model on mount without calling informChange', () => {
        seedDraft();
        const informChange = jest.fn();

        render(<AutoReplyAnalysisModelField informChange={informChange}/>);

        expect((screen.getByTestId('autoreply-analysis-model') as HTMLInputElement).value).toBe('saved-model');
        expect(informChange).not.toHaveBeenCalled();
    });

    test('associates the input with its label', () => {
        seedDraft();

        render(<AutoReplyAnalysisModelField informChange={jest.fn()}/>);

        expect(screen.getByLabelText('Analysis model')).toBe(screen.getByTestId('autoreply-analysis-model'));
    });

    test('typing reports the value via informChange and updates the display', () => {
        seedDraft();
        const informChange = jest.fn();
        render(<AutoReplyAnalysisModelField informChange={informChange}/>);

        fireEvent.change(screen.getByTestId('autoreply-analysis-model'), {target: {value: 'gpt-4.1'}});

        expect(informChange).toHaveBeenCalledTimes(1);
        expect(informChange).toHaveBeenCalledWith('analysis_model', 'gpt-4.1');
        expect((screen.getByTestId('autoreply-analysis-model') as HTMLInputElement).value).toBe('gpt-4.1');
    });

    test('a remote draft update refreshes the display when the user has not edited locally', () => {
        seedDraft();
        render(<AutoReplyAnalysisModelField informChange={jest.fn()}/>);

        act(() => {
            seedDraft({instructions: 'saved instructions', analysis_model: 'remote-model'});
        });

        expect((screen.getByTestId('autoreply-analysis-model') as HTMLInputElement).value).toBe('remote-model');
    });

    test('a remote draft update does not override a local edit', () => {
        seedDraft();
        render(<AutoReplyAnalysisModelField informChange={jest.fn()}/>);

        fireEvent.change(screen.getByTestId('autoreply-analysis-model'), {target: {value: 'local-model'}});

        act(() => {
            seedDraft({instructions: 'saved instructions', analysis_model: 'remote-model'});
        });

        expect((screen.getByTestId('autoreply-analysis-model') as HTMLInputElement).value).toBe('local-model');
    });

    test('renders nothing when no draft is hydrated', () => {
        const informChange = jest.fn();

        const {container} = render(<AutoReplyAnalysisModelField informChange={informChange}/>);

        expect(container.firstChild).toBeNull();
        expect(screen.queryByTestId('autoreply-analysis-model')).toBeNull();
        expect(informChange).not.toHaveBeenCalled();
    });
});

// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen, within} from '@testing-library/react';

import {LLMBot} from '@/bots';
import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import manifest from '@/manifest';

import {AutoReplyAgentPicker} from './autoreply_agent_picker';
import {
    ChannelAutoReplySaveErrorKind,
    getChannelAutoReplyDraft,
    setChannelAutoReplyDraft,
} from './autoreply_state';

// mm_webapp reads window.Components/ProductApi at module load, which are absent
// in jsdom. Stub it so importing the bots/picker chain doesn't throw.
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

// react-bootstrap is a webpack external provided by the host at runtime; the
// picker import chain reaches it but never renders it here.
jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

// @/client is the real HTTP boundary; the picker only reads profile picture
// URLs, and useBotlist would fetch when the redux cache is null.
const mockGetAIBots = jest.fn();
jest.mock('@/client', () => ({
    getAIBots: (...args: unknown[]) => mockGetAIBots(...args),
    savePreferences: jest.fn(),
    getChannelAutoReply: jest.fn(),
    updateChannelAutoReply: jest.fn(),
    getProfilePictureUrl: jest.fn((id: string) => `http://example.com/pic/${id}`),
}));

const CHANNEL_ID = 'chan1';
const botsKey = `plugins-${manifest.id}`;

function makeBot(id: string, overrides: Partial<LLMBot> = {}): LLMBot {
    return {
        id,
        displayName: id,
        username: id,
        lastIconUpdate: 0,
        dmChannelID: '',
        channelAccessLevel: ChannelAccessLevel.All,
        channelIDs: null,
        userAccessLevel: UserAccessLevel.All,
        userIDs: null,
        enabledMCPTools: null,
        autoEnableNewMCPTools: false,
        ...overrides,
    };
}

const alpha = makeBot('alpha', {isDefault: true});
const beta = makeBot('beta');

function makeStore(bots: LLMBot[] | null) {
    const initial = {
        [botsKey]: {bots},
        entities: {
            users: {currentUserId: 'me'},
            preferences: {myPreferences: {}},
        },
    };
    return createStore(() => initial);
}

function seedDraft(botId: string, saveError: ChannelAutoReplySaveErrorKind | null = null) {
    setChannelAutoReplyDraft({
        channelId: CHANNEL_ID,
        saved: {bot_id: botId, mode: 'root_posts'},
        saveError,
    });
}

function renderPicker(informChange: (name: string, value: string) => void = jest.fn(), bots: LLMBot[] | null = [alpha, beta]) {
    return render(
        <Provider store={makeStore(bots)}>
            <AutoReplyAgentPicker informChange={informChange}/>
        </Provider>,
    );
}

function pickAgent(displayName: string) {
    fireEvent.click(screen.getByTestId('autoreply-agent-picker'));
    fireEvent.click(within(screen.getByTestId('dropdownmenu')).getByText(displayName));
}

beforeEach(() => {
    setChannelAutoReplyDraft(null);
    mockGetAIBots.mockReset().mockResolvedValue(null);
});

describe('AutoReplyAgentPicker', () => {
    test('shows the draft-saved agent as selected on mount without calling informChange', () => {
        seedDraft('alpha');
        const informChange = jest.fn();

        renderPicker(informChange);

        expect(screen.getByTestId('autoreply-agent-picker').textContent).toContain('alpha');
        expect(informChange).not.toHaveBeenCalled();
    });

    test('marks the selected agent avatar as decorative with an empty alt', () => {
        seedDraft('alpha');

        renderPicker();

        const avatar = screen.getByTestId('autoreply-agent-picker').querySelector('img');
        expect(avatar).not.toBeNull();
        expect(avatar!.getAttribute('alt')).toBe('');
    });

    test('labels the dropdown trigger with the field label and the current selection', () => {
        seedDraft('alpha');

        renderPicker();

        const trigger = screen.getByTestId('autoreply-agent-picker');
        const labelledBy = trigger.getAttribute('aria-labelledby');
        expect(labelledBy).not.toBeNull();

        const referencedText = labelledBy!.
            split(' ').
            map((id) => document.getElementById(id)?.textContent ?? '').
            join(' ');
        expect(referencedText).toContain('Auto-replying agent');
        expect(referencedText).toContain('alpha');
    });

    test('shows the placeholder when the draft has no agent to display', () => {
        seedDraft('');

        renderPicker();

        expect(screen.getByTestId('autoreply-agent-picker').textContent).toContain('Select an agent');
    });

    test('picking an agent reports it via informChange and updates the display', () => {
        seedDraft('alpha');
        const informChange = jest.fn();
        renderPicker(informChange);

        pickAgent('beta');

        expect(informChange).toHaveBeenCalledTimes(1);
        expect(informChange).toHaveBeenCalledWith('bot_id', 'beta');
        expect(screen.getByTestId('autoreply-agent-picker').textContent).toContain('beta');
    });

    test.each([
        {kind: 'forbidden' as const, message: 'You don’t have permission to change auto-reply settings for this channel.'},
        {kind: 'no_agent' as const, message: 'Select an agent to enable automatic replies.'},
        {kind: 'generic' as const, message: 'Failed to save auto-reply settings. Please try again.'},
    ])('renders the $kind save error message', ({kind, message}) => {
        seedDraft('alpha', kind);

        renderPicker();

        expect(screen.getByText(message)).not.toBeNull();
    });

    test('picking an agent clears the displayed save error', () => {
        seedDraft('alpha', 'generic');
        renderPicker();
        expect(screen.getByText('Failed to save auto-reply settings. Please try again.')).not.toBeNull();

        pickAgent('beta');

        expect(screen.queryByText('Failed to save auto-reply settings. Please try again.')).toBeNull();
        expect(getChannelAutoReplyDraft()?.saveError).toBeNull();
    });

    test('renders the load-failure message when no draft is hydrated', () => {
        const informChange = jest.fn();

        renderPicker(informChange);

        expect(screen.getByText('Auto-reply settings could not be loaded. Close the dialog and try again.')).not.toBeNull();
        expect(screen.queryByTestId('autoreply-agent-picker')).toBeNull();
        expect(informChange).not.toHaveBeenCalled();
    });

    test('a remote draft update refreshes the display when the user has not picked locally', () => {
        seedDraft('alpha');
        renderPicker();
        expect(screen.getByTestId('autoreply-agent-picker').textContent).toContain('alpha');

        act(() => {
            setChannelAutoReplyDraft({
                channelId: CHANNEL_ID,
                saved: {bot_id: 'beta', mode: 'threads'},
                saveError: null,
            });
        });

        expect(screen.getByTestId('autoreply-agent-picker').textContent).toContain('beta');
    });

    test('a remote draft update does not override a local pick', () => {
        seedDraft('alpha');
        renderPicker();

        pickAgent('beta');

        act(() => {
            setChannelAutoReplyDraft({
                channelId: CHANNEL_ID,
                saved: {bot_id: 'alpha', mode: 'root_posts'},
                saveError: null,
            });
        });

        expect(screen.getByTestId('autoreply-agent-picker').textContent).toContain('beta');
    });
});

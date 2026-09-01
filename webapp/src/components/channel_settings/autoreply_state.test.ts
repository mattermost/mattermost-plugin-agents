// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ChannelAutoReplySettings, getChannelAutoReply} from '@/client';
import {LLMBot} from '@/bots';
import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';

import {
    getChannelAutoReplyDraft,
    handleChannelAutoReplyUpdated,
    normalizeChannelAutoReply,
    parseChannelAutoReplyMode,
    setChannelAutoReplyDraft,
    setChannelAutoReplySaveError,
    subscribeChannelAutoReplyDraft,
} from './autoreply_state';

// mm_webapp reads window.Components/ProductApi at module load, which are absent
// in jsdom. Stub it so importing bots.tsx's redux chain doesn't throw.
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

// @/client is the real HTTP boundary; stub it so the websocket handler's
// re-fetch is observable without network access.
jest.mock('@/client', () => ({
    getChannelAutoReply: jest.fn(),
}));

const mockedGetChannelAutoReply = getChannelAutoReply as jest.MockedFunction<typeof getChannelAutoReply>;

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

const CHANNEL_ID = 'chan1';

function settings(overrides: Partial<ChannelAutoReplySettings> = {}): ChannelAutoReplySettings {
    return {
        bot_id: 'alpha',
        mode: 'root_posts',
        instructions: '',
        analysis_model: '',
        ...overrides,
    };
}

function makeDraft(overrides: Partial<{channelId: string; saved: ChannelAutoReplySettings; saveError: 'forbidden' | 'no_agent' | 'generic' | null}> = {}) {
    return {
        channelId: CHANNEL_ID,
        saved: settings(),
        saveError: null,
        ...overrides,
    };
}

beforeEach(() => {
    setChannelAutoReplyDraft(null);
    mockedGetChannelAutoReply.mockReset();
});

describe('draft store', () => {
    test('set/get round-trips and notifies subscribers', () => {
        const subscriber = jest.fn();
        const unsubscribe = subscribeChannelAutoReplyDraft(subscriber);

        const draft = makeDraft();
        setChannelAutoReplyDraft(draft);

        expect(getChannelAutoReplyDraft()).toBe(draft);
        expect(subscriber).toHaveBeenCalledTimes(1);
        unsubscribe();
    });

    test('unsubscribe stops notifications', () => {
        const subscriber = jest.fn();
        const unsubscribe = subscribeChannelAutoReplyDraft(subscriber);

        unsubscribe();
        setChannelAutoReplyDraft(makeDraft());

        expect(subscriber).not.toHaveBeenCalled();
    });

    test('a throwing subscriber does not block other subscribers', () => {
        const bad = jest.fn(() => {
            throw new Error('boom');
        });
        const good = jest.fn();
        const unsubscribeBad = subscribeChannelAutoReplyDraft(bad);
        const unsubscribeGood = subscribeChannelAutoReplyDraft(good);

        setChannelAutoReplyDraft(makeDraft());

        expect(bad).toHaveBeenCalledTimes(1);
        expect(good).toHaveBeenCalledTimes(1);
        unsubscribeBad();
        unsubscribeGood();
    });

    test('setChannelAutoReplySaveError updates the error on the hydrated draft and notifies', () => {
        setChannelAutoReplyDraft(makeDraft());
        const subscriber = jest.fn();
        const unsubscribe = subscribeChannelAutoReplyDraft(subscriber);

        setChannelAutoReplySaveError('forbidden');

        expect(getChannelAutoReplyDraft()).toMatchObject({channelId: CHANNEL_ID, saveError: 'forbidden'});
        expect(subscriber).toHaveBeenCalledTimes(1);

        setChannelAutoReplySaveError(null);
        expect(getChannelAutoReplyDraft()?.saveError).toBeNull();
        unsubscribe();
    });

    test('setChannelAutoReplySaveError is a no-op when no draft is hydrated', () => {
        const subscriber = jest.fn();
        const unsubscribe = subscribeChannelAutoReplyDraft(subscriber);

        setChannelAutoReplySaveError('generic');

        expect(getChannelAutoReplyDraft()).toBeNull();
        expect(subscriber).not.toHaveBeenCalled();
        unsubscribe();
    });
});

describe('parseChannelAutoReplyMode', () => {
    test.each([
        {raw: 'off', want: 'off'},
        {raw: 'root_posts', want: 'root_posts'},
        {raw: 'threads', want: 'threads'},
        {raw: 'ambient', want: 'ambient'},
        {raw: 'banana', want: 'off'},
        {raw: '', want: 'off'},
        // eslint-disable-next-line no-undefined
        {raw: undefined, want: 'off'},
    ])('maps $raw to $want', ({raw, want}) => {
        expect(parseChannelAutoReplyMode(raw)).toBe(want);
    });
});

describe('normalizeChannelAutoReply', () => {
    const defaultBot = makeBot('def', {isDefault: true});
    const other = makeBot('other');
    const bots = [other, defaultBot];

    test.each([
        {name: 'unknown mode', raw: {bot_id: 'other', mode: 'banana'}},
        // eslint-disable-next-line no-undefined
        {name: 'missing mode', raw: {bot_id: 'other', mode: undefined}},
        {name: 'empty mode', raw: {bot_id: 'other', mode: ''}},
    ])('collapses $name to off', ({raw}) => {
        const normalized = normalizeChannelAutoReply(raw as unknown as ChannelAutoReplySettings, bots, CHANNEL_ID);
        expect(normalized.mode).toBe('off');
    });

    test.each([
        {mode: 'root_posts' as const},
        {mode: 'threads' as const},
        {mode: 'off' as const},
        {mode: 'ambient' as const},
    ])('keeps the known mode $mode', ({mode}) => {
        const normalized = normalizeChannelAutoReply(settings({bot_id: 'other', mode}), bots, CHANNEL_ID);
        expect(normalized.mode).toBe(mode);
    });

    test('keeps the saved bot when it is available in the channel', () => {
        const normalized = normalizeChannelAutoReply(settings({bot_id: 'other', mode: 'threads'}), bots, CHANNEL_ID);
        expect(normalized.bot_id).toBe('other');
    });

    test('falls back to the default agent when the saved bot no longer exists', () => {
        const normalized = normalizeChannelAutoReply(settings({bot_id: 'deleted', mode: 'threads'}), bots, CHANNEL_ID);
        expect(normalized.bot_id).toBe('def');
    });

    test('falls back to the default agent when the saved bot is filtered out of the channel', () => {
        const blocked = makeBot('blocked', {channelAccessLevel: ChannelAccessLevel.Allow, channelIDs: ['some-other-channel']});
        const normalized = normalizeChannelAutoReply(settings({bot_id: 'blocked', mode: 'threads'}), [blocked, defaultBot], CHANNEL_ID);
        expect(normalized.bot_id).toBe('def');
    });

    test('falls back to the first agent when no default exists', () => {
        const normalized = normalizeChannelAutoReply(settings({bot_id: 'deleted', mode: 'threads'}), [other], CHANNEL_ID);
        expect(normalized.bot_id).toBe('other');
    });

    test('resolves the default agent when bot_id is unset', () => {
        const normalized = normalizeChannelAutoReply(settings({bot_id: '', mode: 'off'}), bots, CHANNEL_ID);
        expect(normalized.bot_id).toBe('def');
    });

    test('tolerates a missing bot_id field', () => {
        // eslint-disable-next-line no-undefined
        const normalized = normalizeChannelAutoReply({bot_id: undefined, mode: 'off'} as unknown as ChannelAutoReplySettings, bots, CHANNEL_ID);
        expect(normalized.bot_id).toBe('def');
    });

    test('returns an empty bot_id when no agents are available in the channel, preserving extras', () => {
        const normalized = normalizeChannelAutoReply(settings({
            bot_id: 'other',
            mode: 'threads',
            instructions: 'keep me',
            analysis_model: 'gpt-4.1',
        }), [], CHANNEL_ID);
        expect(normalized).toEqual({
            bot_id: '',
            mode: 'threads',
            instructions: 'keep me',
            analysis_model: 'gpt-4.1',
        });
    });

    test('preserves the saved bot (while still validating the mode) when the bot list is unknown', () => {
        const normalized = normalizeChannelAutoReply({bot_id: 'other', mode: 'banana'} as unknown as ChannelAutoReplySettings, null, CHANNEL_ID);
        expect(normalized).toEqual({bot_id: 'other', mode: 'off', instructions: '', analysis_model: ''});
    });

    test('tolerates a missing bot_id when the bot list is unknown', () => {
        // eslint-disable-next-line no-undefined
        const normalized = normalizeChannelAutoReply({bot_id: undefined, mode: 'threads'} as unknown as ChannelAutoReplySettings, null, CHANNEL_ID);
        expect(normalized).toEqual({bot_id: '', mode: 'threads', instructions: '', analysis_model: ''});
    });

    test('fills empty extras when instructions and analysis_model are missing', () => {
        const normalized = normalizeChannelAutoReply({bot_id: 'other', mode: 'ambient'} as unknown as ChannelAutoReplySettings, bots, CHANNEL_ID);
        expect(normalized).toEqual({bot_id: 'other', mode: 'ambient', instructions: '', analysis_model: ''});
    });

    test('keeps ambient extras when they are present', () => {
        const normalized = normalizeChannelAutoReply(settings({
            bot_id: 'other',
            mode: 'ambient',
            instructions: 'only if asked',
            analysis_model: 'gpt-4.1',
        }), bots, CHANNEL_ID);
        expect(normalized).toEqual({
            bot_id: 'other',
            mode: 'ambient',
            instructions: 'only if asked',
            analysis_model: 'gpt-4.1',
        });
    });
});

describe('handleChannelAutoReplyUpdated', () => {
    const defaultBot = makeBot('def', {isDefault: true});
    const other = makeBot('other');
    const getBots = () => [defaultBot, other];

    test('re-fetches and updates the draft (clearing saveError) when the event targets the hydrated channel', async () => {
        setChannelAutoReplyDraft(makeDraft({saved: settings({bot_id: 'def', mode: 'off'}), saveError: 'generic'}));
        mockedGetChannelAutoReply.mockResolvedValue(settings({bot_id: 'other', mode: 'threads'}));

        await handleChannelAutoReplyUpdated(getBots, {channel_id: CHANNEL_ID});

        expect(mockedGetChannelAutoReply).toHaveBeenCalledWith(CHANNEL_ID);
        expect(getChannelAutoReplyDraft()).toEqual({
            channelId: CHANNEL_ID,
            saved: settings({bot_id: 'other', mode: 'threads'}),
            saveError: null,
        });
    });

    test('normalizes untrusted fetched data against the current bots', async () => {
        setChannelAutoReplyDraft(makeDraft({saved: settings({bot_id: 'def', mode: 'off'})}));
        mockedGetChannelAutoReply.mockResolvedValue({bot_id: 'deleted', mode: 'banana'} as unknown as ChannelAutoReplySettings);

        await handleChannelAutoReplyUpdated(getBots, {channel_id: CHANNEL_ID});

        expect(getChannelAutoReplyDraft()?.saved).toEqual(settings({bot_id: 'def', mode: 'off'}));
    });

    test('preserves the fetched bot_id when the bots cache is cold during a re-sync', async () => {
        setChannelAutoReplyDraft(makeDraft({saved: settings({bot_id: 'def', mode: 'off'})}));
        mockedGetChannelAutoReply.mockResolvedValue(settings({bot_id: 'other', mode: 'threads'}));

        await handleChannelAutoReplyUpdated(() => null, {channel_id: CHANNEL_ID});

        expect(getChannelAutoReplyDraft()?.saved).toEqual(settings({bot_id: 'other', mode: 'threads'}));
    });

    test('writes ambient extras from a re-fetch into the draft', async () => {
        setChannelAutoReplyDraft(makeDraft({saved: settings({bot_id: 'def', mode: 'off'})}));
        mockedGetChannelAutoReply.mockResolvedValue(settings({
            bot_id: 'other',
            mode: 'ambient',
            instructions: 'only if asked',
            analysis_model: 'gpt-4.1',
        }));

        await handleChannelAutoReplyUpdated(getBots, {channel_id: CHANNEL_ID});

        expect(getChannelAutoReplyDraft()?.saved).toEqual(settings({
            bot_id: 'other',
            mode: 'ambient',
            instructions: 'only if asked',
            analysis_model: 'gpt-4.1',
        }));
    });

    test('does not fetch when the event targets a different channel', async () => {
        const draft = makeDraft();
        setChannelAutoReplyDraft(draft);

        await handleChannelAutoReplyUpdated(getBots, {channel_id: 'someone-else'});

        expect(mockedGetChannelAutoReply).not.toHaveBeenCalled();
        expect(getChannelAutoReplyDraft()).toBe(draft);
    });

    test('does not fetch when no draft is hydrated', async () => {
        await handleChannelAutoReplyUpdated(getBots, {channel_id: CHANNEL_ID});

        expect(mockedGetChannelAutoReply).not.toHaveBeenCalled();
        expect(getChannelAutoReplyDraft()).toBeNull();
    });

    test('does not fetch when the event carries no channel_id', async () => {
        setChannelAutoReplyDraft(makeDraft());

        await handleChannelAutoReplyUpdated(getBots, {});

        expect(mockedGetChannelAutoReply).not.toHaveBeenCalled();
    });

    test('leaves the draft unchanged when the re-fetch fails', async () => {
        const draft = makeDraft({saveError: 'forbidden'});
        setChannelAutoReplyDraft(draft);
        mockedGetChannelAutoReply.mockRejectedValue(new Error('network'));

        await handleChannelAutoReplyUpdated(getBots, {channel_id: CHANNEL_ID});

        expect(getChannelAutoReplyDraft()).toBe(draft);
    });

    test('does not clobber a draft re-hydrated for another channel while the fetch was in flight', async () => {
        setChannelAutoReplyDraft(makeDraft());
        let resolveFetch!: (settings: ChannelAutoReplySettings) => void;
        mockedGetChannelAutoReply.mockReturnValue(new Promise((resolve) => {
            resolveFetch = resolve;
        }));

        const handled = handleChannelAutoReplyUpdated(getBots, {channel_id: CHANNEL_ID});

        // The modal re-hydrates for a different channel before the GET settles.
        const newerDraft = makeDraft({channelId: 'newer-channel', saved: settings({bot_id: 'def', mode: 'off'})});
        setChannelAutoReplyDraft(newerDraft);
        resolveFetch(settings({bot_id: 'other', mode: 'threads'}));
        await handled;

        expect(getChannelAutoReplyDraft()).toBe(newerDraft);
    });
});

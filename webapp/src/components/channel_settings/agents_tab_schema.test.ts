// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createStore} from 'redux';
import {IntlShape} from 'react-intl';

import {ClientError} from '@mattermost/client';
import {Channel} from '@mattermost/types/channels';
import {GlobalState} from '@mattermost/types/store';

import {
    ChannelAutoReplySettings,
    getAIBots,
    getChannelAutoReply,
    getChannelInstructions,
    updateChannelAutoReply,
    updateChannelInstructions,
} from '@/client';
import {LLMBot} from '@/bots';
import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import {
    PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES,
    PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES,
} from '@/utils/permissions';
import manifest from '@/manifest';

import {
    AgentsTabBaseline,
    ChannelSettingsValues,
    WebappStore,
    makeChannelAgentsTabSchema,
    makeLoadValues,
    makeOnSave,
    shouldRenderChannelAgentsTab,
} from './agents_tab_schema';
import {MAX_INSTRUCTIONS_RUNES, getChannelAutoReplyDraft, setChannelAutoReplyDraft} from './autoreply_state';
import {AutoReplyAgentPicker} from './autoreply_agent_picker';
import {AutoReplyInstructionsField} from './autoreply_ambient_fields';
import {getChannelInstructionsDraft, setChannelInstructionsDraft} from './channel_instructions_state';
import {ChannelInstructionsSetting} from './channel_instructions_setting';
import {ChannelContextPostsSection} from './channel_context_posts_section';

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

// react-bootstrap is a webpack external provided by the host at runtime; the
// picker import chain reaches it but never renders it here.
jest.mock('react-bootstrap', () => ({
    OverlayTrigger: () => null,
    Tooltip: () => null,
}), {virtual: true});

// @/client is the real HTTP boundary.
jest.mock('@/client', () => ({
    getAIBots: jest.fn(),
    savePreferences: jest.fn(),
    getChannelAutoReply: jest.fn(),
    updateChannelAutoReply: jest.fn(),
    getChannelInstructions: jest.fn(),
    updateChannelInstructions: jest.fn(),
    getProfilePictureUrl: jest.fn(() => ''),
}));

const mockedGetChannelAutoReply = getChannelAutoReply as jest.MockedFunction<typeof getChannelAutoReply>;
const mockedUpdateChannelAutoReply = updateChannelAutoReply as jest.MockedFunction<typeof updateChannelAutoReply>;
const mockedGetChannelInstructions = getChannelInstructions as jest.MockedFunction<typeof getChannelInstructions>;
const mockedUpdateChannelInstructions = updateChannelInstructions as jest.MockedFunction<typeof updateChannelInstructions>;
const mockedGetAIBots = getAIBots as jest.MockedFunction<typeof getAIBots>;

// Message ids are injected by babel-plugin-formatjs at build time, so plain
// intl.formatMessage({defaultMessage}) has no id under ts-jest; the standard
// stub used across this repo's tests returns the defaultMessage.
const intl = {
    formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
} as unknown as IntlShape;

const botsKey = `plugins-${manifest.id}`;
const TEAM_ID = 'team1';
const CHANNEL_ID = 'chan1';
const emptyExtras = {instructions: ''};
const emptyValues = {...emptyExtras, channel_instructions: ''};

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

function makeChannel(type: string, id = CHANNEL_ID): Channel {
    return {id, type, team_id: TEAM_ID} as Channel;
}

function makeState(opts: {bots: LLMBot[] | null; channelPermissions?: string[]; skuShortName?: string}): GlobalState {
    return {
        entities: {
            general: {config: {}, license: {SkuShortName: opts.skuShortName ?? 'advanced'}},
            users: {currentUserId: 'me', profiles: {me: {roles: 'system_user'}}},
            teams: {myMembers: {[TEAM_ID]: {roles: 'team_user'}}},
            channels: {roles: {[CHANNEL_ID]: new Set(['channel_role'])}},
            roles: {
                roles: {
                    system_user: {permissions: []},
                    team_user: {permissions: []},
                    channel_role: {permissions: opts.channelPermissions ?? []},
                },
            },
        },
        [botsKey]: {bots: opts.bots},
    } as unknown as GlobalState;
}

function makeTestStore(state: GlobalState): WebappStore {
    return createStore(() => state) as unknown as WebappStore;
}

function httpError(status: number): ClientError {
    return new ClientError('http://localhost', {message: '', status_code: status, url: 'u'});
}

beforeEach(() => {
    setChannelAutoReplyDraft(null);
    setChannelInstructionsDraft(null);
    mockedGetChannelAutoReply.mockReset();
    mockedUpdateChannelAutoReply.mockReset();
    mockedGetChannelInstructions.mockReset();
    mockedGetChannelInstructions.mockResolvedValue({instructions: ''});
    mockedUpdateChannelInstructions.mockReset();
    mockedUpdateChannelInstructions.mockImplementation((_, instructions) => Promise.resolve({instructions: instructions.trim()}));
    mockedGetAIBots.mockReset();
});

describe('shouldRenderChannelAgentsTab', () => {
    const bot = makeBot('alpha');
    const filteredOut = makeBot('elsewhere', {channelAccessLevel: ChannelAccessLevel.Allow, channelIDs: ['some-other-channel']});
    const bothPerms = [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES, PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES];

    test.each([
        {name: 'DM channel even with permissions and agents', type: 'D', perms: bothPerms, bots: [bot], want: false},
        {name: 'GM channel even with permissions and agents', type: 'G', perms: bothPerms, bots: [bot], want: false},
        {name: 'open channel without any manage permission', type: 'O', perms: [], bots: [bot], want: false},
        {name: 'open channel with only the private manage permission', type: 'O', perms: [PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES], bots: [bot], want: false},
        {name: 'private channel without any manage permission', type: 'P', perms: [], bots: [bot], want: false},
        {name: 'private channel with only the public manage permission', type: 'P', perms: [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES], bots: [bot], want: false},
        {name: 'open channel with permission but a cold bots cache', type: 'O', perms: bothPerms, bots: null, want: false},
        {name: 'open channel with permission but zero agents', type: 'O', perms: bothPerms, bots: [], want: false},
        {name: 'open channel with permission but all agents filtered out', type: 'O', perms: bothPerms, bots: [filteredOut], want: false},
        {name: 'open channel with the public manage permission and an agent', type: 'O', perms: [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES], bots: [bot], want: true},
        {name: 'private channel with the private manage permission and an agent', type: 'P', perms: [PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES], bots: [bot], want: true},
        {name: 'open channel with permission and an agent on an Enterprise license', type: 'O', perms: bothPerms, bots: [bot], sku: 'enterprise', want: false},
        {name: 'open channel with permission and an agent without a license', type: 'O', perms: bothPerms, bots: [bot], sku: '', want: false},
    ])('$name -> $want', ({type, perms, bots, sku, want}: {type: string; perms: string[]; bots: LLMBot[] | null; sku?: string; want: boolean}) => {
        const state = makeState({bots, channelPermissions: perms, skuShortName: sku});
        expect(shouldRenderChannelAgentsTab(state, makeChannel(type))).toBe(want);
    });
});

describe('makeLoadValues', () => {
    const defaultBot = makeBot('def', {isDefault: true});
    const other = makeBot('other');

    test('returns the normalized GET payload and seeds the draft store', async () => {
        mockedGetChannelAutoReply.mockResolvedValue({bot_id: 'other', mode: 'threads', ...emptyExtras});
        const store = makeTestStore(makeState({bots: [defaultBot, other]}));

        const values = await makeLoadValues(store, {values: null})(makeChannel('O'));

        expect(mockedGetChannelAutoReply).toHaveBeenCalledWith(CHANNEL_ID);
        expect(values).toEqual({mode: 'threads', bot_id: 'other', ...emptyValues});
        expect(getChannelAutoReplyDraft()).toEqual({
            channelId: CHANNEL_ID,
            saved: {bot_id: 'other', mode: 'threads', ...emptyExtras},
            saveError: null,
        });
        expect(mockedGetAIBots).not.toHaveBeenCalled();
    });

    test('hydrates the channel instructions, seeds their draft, and records the baseline', async () => {
        mockedGetChannelAutoReply.mockResolvedValue({bot_id: 'other', mode: 'off', ...emptyExtras});
        mockedGetChannelInstructions.mockResolvedValue({instructions: 'Deploys freeze on Fridays.'});
        const store = makeTestStore(makeState({bots: [other]}));
        const baseline: AgentsTabBaseline = {values: null};

        const values = await makeLoadValues(store, baseline)(makeChannel('O'));

        expect(mockedGetChannelInstructions).toHaveBeenCalledWith(CHANNEL_ID);
        expect(values).toEqual({mode: 'off', bot_id: 'other', instructions: '', channel_instructions: 'Deploys freeze on Fridays.'});
        expect(getChannelInstructionsDraft()).toEqual({channelId: CHANNEL_ID, saved: 'Deploys freeze on Fridays.', saveError: null});
        expect(baseline.values).toEqual(values);
    });

    test('resolves the default agent when the setting is unset', async () => {
        mockedGetChannelAutoReply.mockResolvedValue({bot_id: '', mode: 'off', ...emptyExtras});
        const store = makeTestStore(makeState({bots: [other, defaultBot]}));

        const values = await makeLoadValues(store, {values: null})(makeChannel('O'));

        expect(values).toEqual({mode: 'off', bot_id: 'def', ...emptyValues});
    });

    test('resolves the single agent when exactly one is available', async () => {
        mockedGetChannelAutoReply.mockResolvedValue({bot_id: '', mode: 'off', ...emptyExtras});
        const store = makeTestStore(makeState({bots: [other]}));

        const values = await makeLoadValues(store, {values: null})(makeChannel('O'));

        expect(values).toEqual({mode: 'off', bot_id: 'other', ...emptyValues});
    });

    test('awaits a bots fetch when the runtime cache is null', async () => {
        mockedGetChannelAutoReply.mockResolvedValue({bot_id: '', mode: 'off', ...emptyExtras});
        mockedGetAIBots.mockResolvedValue({bots: [other], searchEnabled: false, allowUnsafeLinks: false});
        const store = makeTestStore(makeState({bots: null}));

        const values = await makeLoadValues(store, {values: null})(makeChannel('O'));

        expect(mockedGetAIBots).toHaveBeenCalled();
        expect(values).toEqual({mode: 'off', bot_id: 'other', ...emptyValues});
    });

    test('preserves the saved agent when the bots fetch fails', async () => {
        mockedGetChannelAutoReply.mockResolvedValue({bot_id: 'other', mode: 'threads'} as ChannelAutoReplySettings);
        mockedGetAIBots.mockRejectedValue(new Error('bots unavailable'));
        const store = makeTestStore(makeState({bots: null}));

        const values = await makeLoadValues(store, {values: null})(makeChannel('O'));

        expect(values).toEqual({mode: 'threads', bot_id: 'other', ...emptyValues});
        expect(getChannelAutoReplyDraft()?.saved).toEqual({bot_id: 'other', mode: 'threads', ...emptyExtras});
    });

    test('clears the saved agent when the fetch returns a genuinely empty agent list', async () => {
        mockedGetChannelAutoReply.mockResolvedValue({bot_id: 'other', mode: 'threads'} as ChannelAutoReplySettings);
        mockedGetAIBots.mockResolvedValue({bots: [], searchEnabled: false, allowUnsafeLinks: false});
        const store = makeTestStore(makeState({bots: null}));

        const values = await makeLoadValues(store, {values: null})(makeChannel('O'));

        expect(values).toEqual({mode: 'threads', bot_id: '', ...emptyValues});
    });

    test('returns ambient extras from GET and seeds them on the draft', async () => {
        mockedGetChannelAutoReply.mockResolvedValue({
            bot_id: 'other',
            mode: 'ambient',
            instructions: 'only if asked',
        });
        const store = makeTestStore(makeState({bots: [other]}));

        const values = await makeLoadValues(store, {values: null})(makeChannel('O'));

        expect(values).toEqual({
            mode: 'ambient',
            bot_id: 'other',
            instructions: 'only if asked',
            channel_instructions: '',
        });
        expect(getChannelAutoReplyDraft()?.saved).toEqual({
            bot_id: 'other',
            mode: 'ambient',
            instructions: 'only if asked',
        });
    });

    test.each([
        {name: 'auto-reply GET', failAutoReply: true},
        {name: 'instructions GET', failAutoReply: false},
    ])('clears any stale drafts and baseline and rejects when the $name fails', async ({failAutoReply}) => {
        setChannelAutoReplyDraft({channelId: 'previous-channel', saved: {bot_id: 'x', mode: 'off', ...emptyExtras}, saveError: null});
        setChannelInstructionsDraft({channelId: 'previous-channel', saved: 'old', saveError: null});
        const baseline: AgentsTabBaseline = {values: {mode: 'off', bot_id: 'x', instructions: '', channel_instructions: 'old'}};
        const error = httpError(403);
        if (failAutoReply) {
            mockedGetChannelAutoReply.mockRejectedValue(error);
        } else {
            mockedGetChannelAutoReply.mockResolvedValue({bot_id: '', mode: 'off', ...emptyExtras});
            mockedGetChannelInstructions.mockRejectedValue(error);
        }
        const store = makeTestStore(makeState({bots: [other]}));

        await expect(makeLoadValues(store, baseline)(makeChannel('O'))).rejects.toBe(error);

        expect(getChannelAutoReplyDraft()).toBeNull();
        expect(getChannelInstructionsDraft()).toBeNull();
        expect(baseline.values).toBeNull();
    });
});

describe('makeLoadValues overlapping hydrations', () => {
    const other = makeBot('other');

    function pending<T>() {
        let resolve!: (value: T) => void;
        let reject!: (reason: unknown) => void;
        const promise = new Promise<T>((res, rej) => {
            resolve = res;
            reject = rej;
        });
        return {promise, resolve, reject};
    }

    test('a slow earlier load cannot replace the drafts or baseline of a later one', async () => {
        const slowAutoReply = pending<ChannelAutoReplySettings>();
        mockedGetChannelAutoReply.
            mockReturnValueOnce(slowAutoReply.promise).
            mockResolvedValueOnce({bot_id: 'other', mode: 'threads', ...emptyExtras});
        mockedGetChannelInstructions.
            mockResolvedValueOnce({instructions: 'channel A'}).
            mockResolvedValueOnce({instructions: 'channel B'});
        const baseline: AgentsTabBaseline = {values: null};
        const load = makeLoadValues(makeTestStore(makeState({bots: [other]})), baseline);

        const first = load(makeChannel('O', 'chanA'));
        await load(makeChannel('O', 'chanB'));
        slowAutoReply.resolve({bot_id: 'other', mode: 'off', ...emptyExtras});
        await first;

        expect(getChannelAutoReplyDraft()?.channelId).toBe('chanB');
        expect(getChannelInstructionsDraft()).toEqual({channelId: 'chanB', saved: 'channel B', saveError: null});
        expect(baseline.values?.channel_instructions).toBe('channel B');
    });

    test('a late failure of an earlier load does not clear the later load', async () => {
        const slowAutoReply = pending<ChannelAutoReplySettings>();
        mockedGetChannelAutoReply.
            mockReturnValueOnce(slowAutoReply.promise).
            mockResolvedValueOnce({bot_id: 'other', mode: 'threads', ...emptyExtras});
        mockedGetChannelInstructions.mockResolvedValue({instructions: 'channel B'});
        const baseline: AgentsTabBaseline = {values: null};
        const load = makeLoadValues(makeTestStore(makeState({bots: [other]})), baseline);

        const first = load(makeChannel('O', 'chanA'));
        await load(makeChannel('O', 'chanB'));
        slowAutoReply.reject(httpError(500));
        await expect(first).rejects.toBeDefined();

        expect(getChannelInstructionsDraft()?.channelId).toBe('chanB');
        expect(baseline.values).not.toBeNull();
    });
});

describe('makeOnSave', () => {
    const onSave = makeOnSave({values: null});

    function seedDraft(saveError: 'forbidden' | 'no_agent' | 'generic' | null = null) {
        setChannelAutoReplyDraft({channelId: CHANNEL_ID, saved: {bot_id: 'other', mode: 'off', ...emptyExtras}, saveError});
    }

    test('PUTs the selected mode and agent, then updates the draft and clears the error', async () => {
        seedDraft('generic');
        mockedUpdateChannelAutoReply.mockImplementation(() => Promise.resolve());

        await onSave({mode: 'root_posts', bot_id: 'other'}, makeChannel('O'));

        expect(mockedUpdateChannelAutoReply).toHaveBeenCalledWith(CHANNEL_ID, {bot_id: 'other', mode: 'root_posts', ...emptyExtras});
        expect(getChannelAutoReplyDraft()).toEqual({
            channelId: CHANNEL_ID,
            saved: {bot_id: 'other', mode: 'root_posts', ...emptyExtras},
            saveError: null,
        });
    });

    test.each([
        {name: 'off', values: {mode: 'off', bot_id: 'other', instructions: 'stale'}},
        {name: 'an unknown mode', values: {mode: 'banana', bot_id: 'other', instructions: 'stale'}},
        {name: 'a missing mode', values: {bot_id: 'other', instructions: 'stale'}},
    ])('PUTs an empty bot_id, empty extras, and mode off for $name', async ({values}) => {
        seedDraft();
        mockedUpdateChannelAutoReply.mockImplementation(() => Promise.resolve());

        await onSave(values as ChannelSettingsValues, makeChannel('O'));

        expect(mockedUpdateChannelAutoReply).toHaveBeenCalledWith(CHANNEL_ID, {bot_id: '', mode: 'off', ...emptyExtras});
        expect(getChannelAutoReplyDraft()?.saved).toEqual({bot_id: '', mode: 'off', ...emptyExtras});
    });

    test('PUTs ambient extras with the selected agent', async () => {
        seedDraft();
        mockedUpdateChannelAutoReply.mockImplementation(() => Promise.resolve());

        await onSave({
            mode: 'ambient',
            bot_id: 'other',
            instructions: 'only if asked',
        }, makeChannel('O'));

        expect(mockedUpdateChannelAutoReply).toHaveBeenCalledWith(CHANNEL_ID, {
            bot_id: 'other',
            mode: 'ambient',
            instructions: 'only if asked',
        });
        expect(getChannelAutoReplyDraft()?.saved).toEqual({
            bot_id: 'other',
            mode: 'ambient',
            instructions: 'only if asked',
        });
    });

    test('PUTs ambient_threads with the shared ambient extras', async () => {
        seedDraft();
        mockedUpdateChannelAutoReply.mockImplementation(() => Promise.resolve());

        await onSave({
            mode: 'ambient_threads',
            bot_id: 'other',
            instructions: 'only if asked',
        }, makeChannel('O'));

        expect(mockedUpdateChannelAutoReply).toHaveBeenCalledWith(CHANNEL_ID, {
            bot_id: 'other',
            mode: 'ambient_threads',
            instructions: 'only if asked',
        });
    });

    test('preserves extras when saving a non-ambient enabled mode', async () => {
        seedDraft();
        mockedUpdateChannelAutoReply.mockImplementation(() => Promise.resolve());

        await onSave({
            mode: 'threads',
            bot_id: 'other',
            instructions: 'only if asked',
        }, makeChannel('O'));

        expect(mockedUpdateChannelAutoReply).toHaveBeenCalledWith(CHANNEL_ID, {
            bot_id: 'other',
            mode: 'threads',
            instructions: 'only if asked',
        });
    });

    test.each([
        {name: 'an empty bot_id', values: {mode: 'root_posts', bot_id: ''}},
        {name: 'a bot_id absent from the values', values: {mode: 'threads'}},
        {name: 'ambient with an empty bot_id', values: {mode: 'ambient', bot_id: '', instructions: 'x'}},
    ])('rejects with no_agent and performs no PUT for $name', async ({values}) => {
        seedDraft();

        await expect(onSave(values as ChannelSettingsValues, makeChannel('O'))).rejects.toThrow();

        expect(mockedUpdateChannelAutoReply).not.toHaveBeenCalled();
        expect(getChannelAutoReplyDraft()?.saveError).toBe('no_agent');
    });

    test.each([
        {name: 'ASCII at the limit', instructions: 'a'.repeat(MAX_INSTRUCTIONS_RUNES), accepted: true},
        {name: 'ASCII over the limit', instructions: 'a'.repeat(MAX_INSTRUCTIONS_RUNES + 1), accepted: false},
        {name: 'astral characters at the limit', instructions: '😀'.repeat(MAX_INSTRUCTIONS_RUNES), accepted: true},
        {name: 'astral characters over the limit', instructions: '😀'.repeat(MAX_INSTRUCTIONS_RUNES + 1), accepted: false},
    ])('enforces the instructions length limit for $name', async ({instructions, accepted}) => {
        seedDraft();
        mockedUpdateChannelAutoReply.mockImplementation(() => Promise.resolve());

        const result = onSave({mode: 'ambient', bot_id: 'other', instructions}, makeChannel('O'));

        if (accepted) {
            await expect(result).resolves.toBeUndefined();
            expect(mockedUpdateChannelAutoReply).toHaveBeenCalledTimes(1);
        } else {
            await expect(result).rejects.toThrow();
            expect(mockedUpdateChannelAutoReply).not.toHaveBeenCalled();
        }
    });

    test('does not block turning auto-reply off when stale instructions are over the limit', async () => {
        seedDraft();
        mockedUpdateChannelAutoReply.mockImplementation(() => Promise.resolve());

        await onSave({mode: 'off', instructions: 'a'.repeat(MAX_INSTRUCTIONS_RUNES + 1)}, makeChannel('O'));

        expect(mockedUpdateChannelAutoReply).toHaveBeenCalledWith(CHANNEL_ID, {bot_id: '', mode: 'off', ...emptyExtras});
    });

    test('records invalid and re-throws when the PUT fails with a 400', async () => {
        seedDraft();
        const error = new ClientError('http://localhost', {message: '', status_code: 400, url: 'u'});
        mockedUpdateChannelAutoReply.mockRejectedValue(error);

        await expect(onSave({mode: 'ambient', bot_id: 'other', instructions: 'x'}, makeChannel('O'))).rejects.toBe(error);

        expect(getChannelAutoReplyDraft()?.saveError).toBe('invalid');
    });

    test('records forbidden and re-throws when the PUT fails with a 403', async () => {
        seedDraft();
        const error = new ClientError('http://localhost', {message: '', status_code: 403, url: 'u'});
        mockedUpdateChannelAutoReply.mockRejectedValue(error);

        await expect(onSave({mode: 'threads', bot_id: 'other'}, makeChannel('O'))).rejects.toBe(error);

        expect(getChannelAutoReplyDraft()?.saveError).toBe('forbidden');
    });

    test('records ambient_disabled and re-throws when the PUT conflicts with the admin setting', async () => {
        seedDraft();
        const error = new ClientError('http://localhost', {message: '', status_code: 409, url: 'u'});
        mockedUpdateChannelAutoReply.mockRejectedValue(error);

        await expect(onSave({mode: 'ambient_threads', bot_id: 'other'}, makeChannel('O'))).rejects.toBe(error);

        expect(getChannelAutoReplyDraft()?.saveError).toBe('ambient_disabled');
    });

    test('records generic and re-throws when the PUT fails with a 500', async () => {
        seedDraft();
        const error = new ClientError('http://localhost', {message: '', status_code: 500, url: 'u'});
        mockedUpdateChannelAutoReply.mockRejectedValue(error);

        await expect(onSave({mode: 'threads', bot_id: 'other'}, makeChannel('O'))).rejects.toBe(error);

        expect(getChannelAutoReplyDraft()?.saveError).toBe('generic');
    });

    test('records generic when the PUT fails with a non-HTTP error', async () => {
        seedDraft();
        const error = new Error('network down');
        mockedUpdateChannelAutoReply.mockRejectedValue(error);

        await expect(onSave({mode: 'threads', bot_id: 'other'}, makeChannel('O'))).rejects.toBe(error);

        expect(getChannelAutoReplyDraft()?.saveError).toBe('generic');
    });
});

describe('makeOnSave change detection', () => {
    const hydrated = {mode: 'threads', bot_id: 'other', instructions: '', channel_instructions: 'Deploys freeze on Fridays.'};

    function hydrate(): AgentsTabBaseline {
        setChannelAutoReplyDraft({channelId: CHANNEL_ID, saved: {bot_id: 'other', mode: 'threads', ...emptyExtras}, saveError: null});
        setChannelInstructionsDraft({channelId: CHANNEL_ID, saved: hydrated.channel_instructions, saveError: null});
        mockedUpdateChannelAutoReply.mockImplementation(() => Promise.resolve());
        return {values: {...hydrated}};
    }

    test('saving only new instructions leaves the auto-reply setting untouched', async () => {
        const baseline = hydrate();

        await makeOnSave(baseline)({...hydrated, channel_instructions: '  On-call is #payments-oncall. '}, makeChannel('O'));

        expect(mockedUpdateChannelAutoReply).not.toHaveBeenCalled();
        expect(mockedUpdateChannelInstructions).toHaveBeenCalledWith(CHANNEL_ID, '  On-call is #payments-oncall. ');
        expect(getChannelInstructionsDraft()).toEqual({channelId: CHANNEL_ID, saved: 'On-call is #payments-oncall.', saveError: null});
        expect(baseline.values?.channel_instructions).toBe('  On-call is #payments-oncall. ');
    });

    test('saving only a new auto-reply mode leaves the instructions untouched', async () => {
        const baseline = hydrate();

        await makeOnSave(baseline)({...hydrated, mode: 'root_posts'}, makeChannel('O'));

        expect(mockedUpdateChannelAutoReply).toHaveBeenCalledWith(CHANNEL_ID, {bot_id: 'other', mode: 'root_posts', ...emptyExtras});
        expect(mockedUpdateChannelInstructions).not.toHaveBeenCalled();
    });

    test('a remote instructions change is not written back by an unrelated save', async () => {
        const baseline = hydrate();

        // A websocket re-sync updated the draft while the modal was open; the
        // host's value for instructions is still the hydrated one.
        setChannelInstructionsDraft({channelId: CHANNEL_ID, saved: 'Changed elsewhere', saveError: null});
        await makeOnSave(baseline)({...hydrated, mode: 'off'}, makeChannel('O'));

        expect(mockedUpdateChannelInstructions).not.toHaveBeenCalled();
        expect(getChannelInstructionsDraft()?.saved).toBe('Changed elsewhere');
    });

    test('an invalid saved agent does not block saving instructions', async () => {
        const baseline = hydrate();
        mockedUpdateChannelAutoReply.mockRejectedValue(httpError(400));

        await makeOnSave(baseline)({...hydrated, channel_instructions: 'New'}, makeChannel('O'));

        expect(mockedUpdateChannelAutoReply).not.toHaveBeenCalled();
        expect(mockedUpdateChannelInstructions).toHaveBeenCalledWith(CHANNEL_ID, 'New');
    });

    test('a retry after a partial failure re-sends only the failed part', async () => {
        const baseline = hydrate();
        mockedUpdateChannelInstructions.mockRejectedValueOnce(httpError(500));
        const values = {mode: 'root_posts', bot_id: 'other', instructions: '', channel_instructions: 'New'};

        await expect(makeOnSave(baseline)(values, makeChannel('O'))).rejects.toMatchObject({status_code: 500});
        expect(mockedUpdateChannelAutoReply).toHaveBeenCalledTimes(1);
        expect(getChannelInstructionsDraft()?.saveError).toBe('generic');

        await makeOnSave(baseline)(values, makeChannel('O'));
        expect(mockedUpdateChannelAutoReply).toHaveBeenCalledTimes(1);
        expect(mockedUpdateChannelInstructions).toHaveBeenCalledTimes(2);
        expect(getChannelInstructionsDraft()?.saveError).toBeNull();
    });

    test.each([
        {status: 403, kind: 'forbidden'},
        {status: 400, kind: 'invalid'},
        {status: 413, kind: 'invalid'},
        {status: 500, kind: 'generic'},
    ])('records $kind and re-throws when the instructions PUT fails with $status', async ({status, kind}) => {
        const baseline = hydrate();
        const error = httpError(status);
        mockedUpdateChannelInstructions.mockRejectedValue(error);

        await expect(makeOnSave(baseline)({...hydrated, channel_instructions: 'New'}, makeChannel('O'))).rejects.toBe(error);

        expect(getChannelInstructionsDraft()?.saveError).toBe(kind);
        expect(baseline.values?.channel_instructions).toBe(hydrated.channel_instructions);
    });

    test('skips the instructions PUT when hydration failed and the host has no instructions value', async () => {
        mockedUpdateChannelAutoReply.mockImplementation(() => Promise.resolve());

        await makeOnSave({values: null})({mode: 'off', bot_id: ''}, makeChannel('O'));

        expect(mockedUpdateChannelInstructions).not.toHaveBeenCalled();
    });
});

// The host silently drops invalid registrations (console.warn only), so this
// guards the schema against the host's validation rules: non-empty uiName and
// unique section titles, a non-empty radio default matching an option value,
// non-empty option value/text, and real components for custom settings and
// sections.
describe('makeChannelAgentsTabSchema shape', () => {
    const schema = makeChannelAgentsTabSchema(makeTestStore(makeState({bots: []})), intl);

    function settingsAt(index: number) {
        const section = schema.sections[index];
        if (!('settings' in section)) {
            throw new Error(`expected section ${index} to be a settings section`);
        }
        return section.settings;
    }

    test('has a non-empty uiName and the compass icon class', () => {
        expect(typeof schema.uiName).toBe('string');
        expect(schema.uiName.length).toBeGreaterThan(0);
        expect(schema.icon).toBe('icon-creation-outline');
    });

    test('uses the exported shouldRender gate and function callbacks', () => {
        expect(schema.shouldRender).toBe(shouldRenderChannelAgentsTab);
        expect(typeof schema.loadValues).toBe('function');
        expect(typeof schema.onSave).toBe('function');
    });

    test('has auto-reply, instructions, and pinned context sections with unique non-empty titles', () => {
        expect(schema.sections).toHaveLength(3);
        const titles = schema.sections.map((section) => section.title);
        expect(new Set(titles).size).toBe(titles.length);
        for (const title of titles) {
            expect(title.length).toBeGreaterThan(0);
        }
        expect(settingsAt(0).map((s) => s.name)).toEqual(['mode', 'bot_id', 'instructions']);
        expect(settingsAt(1).map((s) => s.name)).toEqual(['channel_instructions']);
    });

    test('instructions setting provides the instructions component', () => {
        const custom = settingsAt(1)[0];
        if (custom.type !== 'custom') {
            throw new Error('expected the instructions setting to be custom');
        }
        expect(custom.component).toBe(ChannelInstructionsSetting);
    });

    test('pinned context section is a custom section component', () => {
        const section = schema.sections[2];
        expect('component' in section && section.component).toBe(ChannelContextPostsSection);
    });

    test('radio setting has default off matching one of five non-empty options', () => {
        const radio = settingsAt(0)[0];
        if (radio.type !== 'radio') {
            throw new Error('expected the first setting to be the radio');
        }
        expect(radio.default).toBe('off');
        expect(radio.options.map((o) => o.value)).toEqual(['off', 'root_posts', 'threads', 'ambient', 'ambient_threads']);
        expect(radio.options.map((o) => o.value)).toContain(radio.default);
        for (const option of radio.options) {
            expect(option.value.length).toBeGreaterThan(0);
            expect(option.text.length).toBeGreaterThan(0);
        }
    });

    test('auto-reply custom settings provide picker and instructions components with no title/helpText/default', () => {
        const expected = [
            {name: 'bot_id', component: AutoReplyAgentPicker},
            {name: 'instructions', component: AutoReplyInstructionsField},
        ];
        const customs = settingsAt(0).slice(1);
        expect(customs).toHaveLength(expected.length);
        for (let i = 0; i < expected.length; i++) {
            const custom = customs[i];
            if (custom.type !== 'custom') {
                throw new Error(`expected setting ${expected[i].name} to be custom`);
            }
            expect(custom.name).toBe(expected[i].name);
            expect(custom.component).toBe(expected[i].component);
            expect(custom).not.toHaveProperty('title');
            expect(custom).not.toHaveProperty('helpText');
            expect(custom).not.toHaveProperty('default');
        }
    });
});

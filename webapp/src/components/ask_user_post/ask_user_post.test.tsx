// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, fireEvent, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';

import {AskUserPost, buildAnswerPreview, parseAskUserProps} from './ask_user_post';

const mockDoAskUserResponse = jest.fn();
const mockGetProfilesByIds = jest.fn();

jest.mock('@/client', () => ({
    doAskUserResponse: (...args: unknown[]) => mockDoAskUserResponse(...args),
    getProfilesByIds: (...args: unknown[]) => mockGetProfilesByIds(...args),
}));

jest.mock('react-redux', () => ({
    useSelector: jest.fn(),
    useDispatch: jest.fn(),
}));

// mm_webapp reads window.Components at module load (absent in jsdom).
jest.mock('@/mm_webapp', () => ({
    Timestamp: null,
}));

const mockUseSelector = useSelector as unknown as jest.Mock;
const mockUseDispatch = useDispatch as unknown as jest.Mock;
const dispatchMock = jest.fn();

// Mattermost IDs are 26 characters of lowercase letters and digits.
const POST_ID = 'c7f2m9xq4v1b8n3k6t5w0hzjd2';
const TARGET_ID = 'kq3n7vd1x9r4bz2m8sw6t5jhpc';
const REQUESTER_ID = 'ehz9k3wqr7t1a5m2xd8pnb4jsc';
const SOURCE_POST_ID = 'a1b2c3d4e5f6g7h8i9j0k1l2m3';
const CONV_ID = 'n4o5p6q7r8s9t0u1v2w3x4y5z6';
const CHANNEL_ID = 'z9y8x7w6v5u4t3s2r1q0p9o8n7';
const BOT_ID = 'b0t1b0t2b0t3b0t4b0t5b0t6b0';
const OTHER_USER_ID = 'o1t2h3e4r5u6s7e8r9i0d1x2y3';

const FALLBACK_MESSAGE = 'Which release broke it? (Open Mattermost in a browser to respond.)';

function makeProps(overrides: Record<string, unknown> = {}): Record<string, unknown> {
    return {
        ask_user_status: 'pending',
        ask_user_question: 'Which release broke it?',
        ask_user_context: 'Needed to finish the RCA',
        ask_user_options: [{label: '4.2.0'}, {label: '4.2.1', description: 'the hotfix'}],
        ask_user_multi_select: false,
        ask_user_allow_free_form: true,
        ask_user_requester_id: REQUESTER_ID,
        ask_user_target_id: TARGET_ID,
        ask_user_conversation_id: CONV_ID,
        ask_user_tool_use_id: 'tooluse_1',
        ask_user_source_post_id: SOURCE_POST_ID,
        ...overrides,
    };
}

function omit(props: Record<string, unknown>, key: string): Record<string, unknown> {
    const out = {...props};
    delete out[key];
    return out;
}

// v2 (V2-C2) prop set: a human requester asking from a DM. Tests override
// individual keys to walk the destination/attribution matrices.
const V2_BASE: Record<string, unknown> = {
    ask_user_requester_kind: 'user',
    ask_user_requester_username: 'jane',
    ask_user_requester_display_name: 'Jane Doe',
    ask_user_requester_position: 'Engineering Lead',
    ask_user_agent_display_name: 'Matty',
    ask_user_destination_type: 'dm',
    ask_user_destination_channel_display_name: '',
    ask_user_destination_member_count: 0,
    ask_user_destination_policy_enforced: false,
};

function makeV2Props(overrides: Record<string, unknown> = {}): Record<string, unknown> {
    return makeProps({...V2_BASE, ...overrides});
}

type StateOverrides = {
    currentUserId?: string;
    profiles?: Record<string, {id: string; username: string}>;
};

function stateFixture(overrides: StateOverrides = {}) {
    return {
        entities: {
            users: {
                currentUserId: overrides.currentUserId ?? TARGET_ID,
                profiles: overrides.profiles ?? {
                    [REQUESTER_ID]: {id: REQUESTER_ID, username: 'jane'},
                    [BOT_ID]: {id: BOT_ID, username: 'agentbot'},
                },
            },
            general: {config: {SiteURL: 'http://localhost:8065'}},
        },
    };
}

function buildPostElement(propOverrides: Record<string, unknown> = {}) {
    return (
        <IntlProvider locale='en'>
            <AskUserPost
                post={{
                    id: POST_ID,
                    message: FALLBACK_MESSAGE,
                    channel_id: CHANNEL_ID,
                    user_id: BOT_ID,
                    props: makeProps(propOverrides),
                }}
            />
        </IntlProvider>
    );
}

function renderPost(propOverrides: Record<string, unknown> = {}) {
    return render(buildPostElement(propOverrides));
}

function renderV2Post(propOverrides: Record<string, unknown> = {}) {
    return renderPost({...V2_BASE, ...propOverrides});
}

beforeEach(() => {
    mockDoAskUserResponse.mockReset();
    mockDoAskUserResponse.mockResolvedValue({status: 'answered'});
    mockGetProfilesByIds.mockReset();
    mockGetProfilesByIds.mockResolvedValue([]);
    dispatchMock.mockReset();
    mockUseDispatch.mockReturnValue(dispatchMock);
    mockUseSelector.mockImplementation((selector) => selector(stateFixture()));
});

describe('parseAskUserProps', () => {
    test('parses well-formed pending props', () => {
        expect(parseAskUserProps(makeProps())).toEqual({
            status: 'pending',
            question: 'Which release broke it?',
            context: 'Needed to finish the RCA',
            options: [{label: '4.2.0'}, {label: '4.2.1', description: 'the hotfix'}],
            multiSelect: false,
            allowFreeForm: true,
            requesterId: REQUESTER_ID,
            targetId: TARGET_ID,
            sourcePostId: SOURCE_POST_ID,
            answeredAt: 0,
            answerPreview: '',
            requesterKind: '',
            requesterUsername: '',
            requesterDisplayName: '',
            requesterPosition: '',
            agentDisplayName: '',
            destinationType: '',
            destinationChannelName: '',
            destinationMemberCount: 0,
            destinationPolicyEnforced: false,
        });
    });

    test.each([
        ['invalid status', makeProps({ask_user_status: 'bogus'})],
        ['missing status', omit(makeProps(), 'ask_user_status')],
        ['empty question', makeProps({ask_user_question: ''})],
        ['missing question', omit(makeProps(), 'ask_user_question')],
        ['missing target id', omit(makeProps(), 'ask_user_target_id')],
        ['non-string target id', makeProps({ask_user_target_id: 42})],
        ['option without a label', makeProps({ask_user_options: [{description: 'no label'}]})],
        ['option with an empty label', makeProps({ask_user_options: [{label: ''}]})],
        ['non-array options', makeProps({ask_user_options: 'not an array'})],
        ['non-object option', makeProps({ask_user_options: ['4.2.0']})],
        ['duplicate option labels', makeProps({ask_user_options: [{label: 'A'}, {label: 'A', description: 'again'}]})],
        ['no options and free-form disabled', makeProps({ask_user_options: [], ask_user_allow_free_form: false})],
    ])('returns null for %s', (_label, props) => {
        expect(parseAskUserProps(props)).toBeNull();
    });

    test('applies defaults for absent optional props', () => {
        let props = makeProps();
        props = omit(props, 'ask_user_context');
        props = omit(props, 'ask_user_requester_id');
        props = omit(props, 'ask_user_source_post_id');
        props = omit(props, 'ask_user_multi_select');
        props = omit(props, 'ask_user_allow_free_form');

        expect(parseAskUserProps(props)).toMatchObject({
            context: '',
            requesterId: '',
            sourcePostId: '',
            answeredAt: 0,
            answerPreview: '',
            allowFreeForm: true,
            multiSelect: false,
        });
    });

    test('treats a non-boolean multi_select as false', () => {
        expect(parseAskUserProps(makeProps({ask_user_multi_select: 'yes'}))?.multiSelect).toBe(false);
    });

    test('treats a non-numeric answered_at as absent', () => {
        expect(parseAskUserProps(makeProps({ask_user_answered_at: 'noon'}))?.answeredAt).toBe(0);
    });

    test('parses the full v2 prop set', () => {
        expect(parseAskUserProps(makeV2Props({
            ask_user_destination_type: 'channel',
            ask_user_destination_channel_display_name: 'Town Square',
            ask_user_destination_member_count: 12,
            ask_user_destination_policy_enforced: true,
        }))).toMatchObject({
            requesterKind: 'user',
            requesterUsername: 'jane',
            requesterDisplayName: 'Jane Doe',
            requesterPosition: 'Engineering Lead',
            agentDisplayName: 'Matty',
            destinationType: 'channel',
            destinationChannelName: 'Town Square',
            destinationMemberCount: 12,
            destinationPolicyEnforced: true,
        });
    });

    test('defaults every v2 prop on a pre-v2 card', () => {
        expect(parseAskUserProps(makeProps())).toMatchObject({
            requesterKind: '',
            requesterUsername: '',
            requesterDisplayName: '',
            requesterPosition: '',
            agentDisplayName: '',
            destinationType: '',
            destinationChannelName: '',
            destinationMemberCount: 0,
            destinationPolicyEnforced: false,
        });
    });

    // A malformed v2 prop must degrade to absent, never break the card.
    test.each([
        ['numeric requester kind', {ask_user_requester_kind: 42}, {requesterKind: ''}],
        ['unknown requester kind string', {ask_user_requester_kind: 'martian'}, {requesterKind: ''}],
        ['unknown destination type', {ask_user_destination_type: 'broadcast'}, {destinationType: ''}],
        ['member count as string', {ask_user_destination_member_count: '12'}, {destinationMemberCount: 0}],
        ['negative member count', {ask_user_destination_member_count: -3}, {destinationMemberCount: 0}],
        ['policy flag as string', {ask_user_destination_policy_enforced: 'yes'}, {destinationPolicyEnforced: false}],
    ])('treats a malformed v2 prop as absent: %s', (_label, overrides, expected) => {
        expect(parseAskUserProps(makeV2Props(overrides))).toMatchObject(expected);
    });

    test('accepts the canceled status', () => {
        expect(parseAskUserProps(makeProps({ask_user_status: 'canceled'}))?.status).toBe('canceled');
    });
});

// Pins the client preview against the server rule (askUserAnswerPreview in
// conversations/ask_another_user.go): labels joined with ', ', ' — ' before
// free-form, 200-rune truncation.
describe('buildAnswerPreview', () => {
    test.each([
        ['labels only', ['A', 'B'], '', 'A, B'],
        ['free-form only', [], 'hello', 'hello'],
        ['labels and free-form joined with an em dash', ['A'], 'extra', 'A — extra'],
        ['whitespace-only free-form dropped', ['A'], '   ', 'A'],
        ['empty', [], '', ''],
    ])('%s', (_label, selected, freeForm, want) => {
        expect(buildAnswerPreview(selected as string[], freeForm as string)).toBe(want);
    });

    test('truncates to 200 runes, not UTF-16 code units', () => {
        // Astral characters are two UTF-16 code units but one rune each.
        const long = '😀'.repeat(300);

        expect(buildAnswerPreview([], long)).toBe('😀'.repeat(200));
    });
});

describe('AskUserPost rendering', () => {
    test('renders the full pending card for the target', () => {
        renderPost();

        expect(screen.getByText('Which release broke it?')).not.toBeNull();
        expect(screen.getByText('Needed to finish the RCA')).not.toBeNull();
        expect(screen.getByText('Asked on behalf of @jane')).not.toBeNull();
        expect(screen.getByText('4.2.0')).not.toBeNull();
        expect(screen.getByText('4.2.1')).not.toBeNull();
        expect(screen.getByText('Answer')).not.toBeNull();
        expect(screen.getByText('Decline')).not.toBeNull();

        const link = screen.getByRole('link', {name: 'View conversation'});
        expect(link.getAttribute('href')).toBe(`http://localhost:8065/_redirect/pl/${SOURCE_POST_ID}`);
    });

    test('pre-v2 cards render the v1 layout without any v2 chrome', () => {
        renderPost();

        expect(screen.queryByText('AI-generated content')).toBeNull();
        expect(screen.queryByText(/Your answer (will|may) be shared/)).toBeNull();
    });

    test('omits the attribution row when there is no requester', () => {
        renderPost({ask_user_requester_id: ''});

        expect(screen.queryByText(/Asked on behalf of/)).toBeNull();
    });

    test('omits the permalink when there is no source post id', () => {
        renderPost({ask_user_source_post_id: ''});

        expect(screen.queryByRole('link', {name: 'View conversation'})).toBeNull();
    });

    test('hydrates the requester and bot profiles when they are not cached', async () => {
        mockUseSelector.mockImplementation((selector) => selector(stateFixture({profiles: {}})));
        mockGetProfilesByIds.mockResolvedValue([
            {id: REQUESTER_ID, username: 'jane'},
            {id: BOT_ID, username: 'agentbot'},
        ]);

        renderPost();

        await waitFor(() => {
            expect(mockGetProfilesByIds).toHaveBeenCalledWith([REQUESTER_ID, BOT_ID]);
            expect(dispatchMock).toHaveBeenCalledWith({
                type: 'RECEIVED_PROFILES',
                data: {
                    [REQUESTER_ID]: expect.objectContaining({username: 'jane'}),
                    [BOT_ID]: expect.objectContaining({username: 'agentbot'}),
                },
            });
        });
    });

    test('renders the answered state from props without controls', () => {
        renderPost({
            ask_user_status: 'answered',
            ask_user_answer_preview: '4.2.1',
            ask_user_answered_at: 1712345678901,
        });

        expect(screen.getByText('Answered')).not.toBeNull();
        expect(screen.getByText('4.2.1')).not.toBeNull();
        expect(screen.queryByText('Answer')).toBeNull();
        expect(screen.queryByText('Decline')).toBeNull();
        expect(screen.queryByText('4.2.0')).toBeNull();
    });

    test('renders the declined state from props without controls', () => {
        renderPost({ask_user_status: 'declined'});

        expect(screen.getByText('You declined to answer')).not.toBeNull();
        expect(screen.queryByText('Answer')).toBeNull();
        expect(screen.queryByText('Decline')).toBeNull();
    });

    test('renders the question without controls for a non-target viewer', () => {
        mockUseSelector.mockImplementation((selector) => selector(stateFixture({currentUserId: OTHER_USER_ID})));

        renderPost();

        expect(screen.getByText('Which release broke it?')).not.toBeNull();
        expect(screen.queryByText('Answer')).toBeNull();
        expect(screen.queryByText('Decline')).toBeNull();
    });

    test('falls back to the post message for malformed props', () => {
        renderPost({ask_user_status: 'bogus'});

        expect(screen.getByText(FALLBACK_MESSAGE)).not.toBeNull();
        expect(screen.queryByText('Answer')).toBeNull();
    });
});

describe('AskUserPost v2 destination disclosure', () => {
    // The full V2-C2 rendering matrix, one row per destination/requester
    // combination.
    test.each([
        [
            'dm from a human requester',
            {},
            'Your answer will be shared with @jane.',
        ],
        [
            'dm from an unattended agent',
            {
                ask_user_requester_kind: 'bot',
                ask_user_requester_username: '',
                ask_user_requester_display_name: '',
                ask_user_requester_position: '',
                ask_user_requester_id: '',
            },
            'Your answer will be shared with the Matty agent.',
        ],
        [
            'dm with an unknown requester',
            {
                ask_user_requester_kind: 'unknown',
                ask_user_requester_username: '',
                ask_user_requester_display_name: '',
                ask_user_requester_position: '',
                ask_user_requester_id: '',
            },
            'Your answer will be shared with the person who asked the agent.',
        ],
        [
            'channel with name and member count',
            {
                ask_user_destination_type: 'channel',
                ask_user_destination_channel_display_name: 'Town Square',
                ask_user_destination_member_count: 12,
            },
            'Your answer may be shared with the 12 members of ~Town Square.',
        ],
        [
            'channel with name only',
            {
                ask_user_destination_type: 'channel',
                ask_user_destination_channel_display_name: 'Town Square',
                ask_user_destination_member_count: 0,
            },
            'Your answer may be shared with the members of ~Town Square.',
        ],
        [
            'channel with nothing known',
            {
                ask_user_destination_type: 'channel',
                ask_user_destination_channel_display_name: '',
                ask_user_destination_member_count: 0,
            },
            'Your answer may be shared in the channel where the agent was asked.',
        ],
        [
            'group message with member count',
            {
                ask_user_destination_type: 'gm',
                ask_user_destination_member_count: 3,
            },
            'Your answer may be shared with the 3 members of a group message.',
        ],
        [
            'group message without member count',
            {
                ask_user_destination_type: 'gm',
                ask_user_destination_member_count: 0,
            },
            'Your answer may be shared with the members of a group message.',
        ],
    ])('%s', (_label, overrides, expected) => {
        renderV2Post(overrides as Record<string, unknown>);

        expect(screen.getByText(expected as string)).not.toBeNull();
    });
});

describe('AskUserPost v2 access context', () => {
    test('renders the policy line only when the flag is true', () => {
        const {unmount} = renderV2Post({
            ask_user_destination_type: 'channel',
            ask_user_destination_channel_display_name: 'Town Square',
            ask_user_destination_member_count: 12,
            ask_user_destination_policy_enforced: true,
        });

        expect(screen.getByText('Access to ~Town Square is restricted by an attribute-based access policy.')).not.toBeNull();
        unmount();

        renderV2Post({
            ask_user_destination_type: 'channel',
            ask_user_destination_channel_display_name: 'Town Square',
            ask_user_destination_member_count: 12,
            ask_user_destination_policy_enforced: false,
        });

        expect(screen.queryByText(/attribute-based access policy/)).toBeNull();
    });

    test('never renders a policy line without a channel name', () => {
        renderV2Post({
            ask_user_destination_type: 'channel',
            ask_user_destination_channel_display_name: '',
            ask_user_destination_policy_enforced: true,
        });

        expect(screen.queryByText(/attribute-based access policy/)).toBeNull();
    });

    test.each([
        ['display name and position', 'Jane Doe', 'Engineering Lead', 'Jane Doe · Engineering Lead'],
        ['display name only', 'Jane Doe', '', 'Jane Doe'],
        ['position only', '', 'Engineering Lead', 'Engineering Lead'],
    ])('identity detail line with %s', (_label, displayName, position, expected) => {
        renderV2Post({
            ask_user_requester_display_name: displayName,
            ask_user_requester_position: position,
        });

        expect(screen.getByText(expected)).not.toBeNull();
    });

    test('omits the identity line when both parts are empty', () => {
        renderV2Post({
            ask_user_requester_display_name: '',
            ask_user_requester_position: '',
        });

        expect(screen.queryByText(/ · /)).toBeNull();
        expect(screen.queryByText('Jane Doe')).toBeNull();
    });
});

describe('AskUserPost v2 attribution', () => {
    test('human requester attributes from props without a profile fetch', () => {
        renderV2Post();

        expect(screen.getByText('Asked on behalf of @jane')).not.toBeNull();

        // The bot profile is cached in the fixture and the requester comes
        // from props, so no hydration request is needed at all.
        expect(mockGetProfilesByIds).not.toHaveBeenCalled();
    });

    test('unattended agent requester renders the unattended copy', () => {
        renderV2Post({
            ask_user_requester_kind: 'bot',
            ask_user_requester_username: '',
            ask_user_requester_display_name: '',
            ask_user_requester_position: '',
            ask_user_requester_id: '',
        });

        expect(screen.getByText('Asked by the Matty agent running unattended (no human requester)')).not.toBeNull();
        expect(screen.queryByText(/Asked on behalf of/)).toBeNull();
    });

    test('unknown requester renders the unknown-requester copy', () => {
        renderV2Post({
            ask_user_requester_kind: 'unknown',
            ask_user_requester_username: '',
            ask_user_requester_display_name: '',
            ask_user_requester_position: '',
            ask_user_requester_id: '',
        });

        expect(screen.getByText('Asked via the Matty agent (requester identity unavailable)')).not.toBeNull();
    });
});

describe('AskUserPost v2 visual separation', () => {
    test('model-authored content renders inside the AI region; system chrome outside', () => {
        renderV2Post();

        const caption = screen.getByText('AI-generated content');
        const aiRegion = caption.parentElement as HTMLElement;

        // Model-authored: question, context, option labels, free-form toggle.
        expect(aiRegion.textContent).toContain('Which release broke it?');
        expect(aiRegion.textContent).toContain('Needed to finish the RCA');
        expect(aiRegion.textContent).toContain('4.2.0');
        expect(aiRegion.textContent).toContain('4.2.1');

        // System-authored chrome must live outside the region so injected
        // text cannot forge it: attribution, disclosure, and the buttons.
        expect(aiRegion.textContent).not.toContain('Asked on behalf of');
        expect(aiRegion.textContent).not.toContain('Your answer will be shared');
        expect(aiRegion.textContent).not.toContain('Answer');
        expect(aiRegion.textContent).not.toContain('Decline');

        // The chrome itself still renders — just elsewhere in the card.
        expect(screen.getByText('Asked on behalf of @jane')).not.toBeNull();
        expect(screen.getByText('Your answer will be shared with @jane.')).not.toBeNull();
        expect(screen.getByText('Answer')).not.toBeNull();
    });
});

describe('AskUserPost canceled state', () => {
    test('canceled props render the neutral terminal line without controls', () => {
        renderV2Post({ask_user_status: 'canceled'});

        expect(screen.getByText('This question is no longer needed.')).not.toBeNull();
        expect(screen.queryByText('Answer')).toBeNull();
        expect(screen.queryByText('Decline')).toBeNull();
        expect(screen.queryByText('Answered')).toBeNull();
        expect(screen.queryByText('4.2.0')).toBeNull();
    });

    test('a pre-v2 canceled card also renders the terminal line', () => {
        renderPost({ask_user_status: 'canceled'});

        expect(screen.getByText('This question is no longer needed.')).not.toBeNull();
        expect(screen.queryByText('Answer')).toBeNull();
    });
});

describe('AskUserPost interaction', () => {
    test('single-select answers with exactly the last clicked option', async () => {
        renderPost();

        fireEvent.click(screen.getByText('4.2.0'));
        fireEvent.click(screen.getByText('4.2.1')); // replaces the prior choice
        fireEvent.click(screen.getByText('Answer'));

        expect(mockDoAskUserResponse).toHaveBeenCalledTimes(1);
        expect(mockDoAskUserResponse).toHaveBeenCalledWith(POST_ID, 'agentbot', {
            action: 'answer',
            selected: ['4.2.1'],
            free_form: '',
        });
        expect(await screen.findByText('Answered')).not.toBeNull();
    });

    test('multi-select accumulates and toggles options off', async () => {
        renderPost({ask_user_multi_select: true});

        fireEvent.click(screen.getByText('4.2.0'));
        fireEvent.click(screen.getByText('4.2.1'));
        fireEvent.click(screen.getByText('4.2.0')); // toggle the first back off
        fireEvent.click(screen.getByText('Answer'));

        expect(mockDoAskUserResponse).toHaveBeenCalledWith(POST_ID, 'agentbot', {
            action: 'answer',
            selected: ['4.2.1'],
            free_form: '',
        });
        expect(await screen.findByText('Answered')).not.toBeNull();
    });

    test('multi-select with an option and free-form text submits both, previewed with an em dash', async () => {
        renderPost({ask_user_multi_select: true});

        fireEvent.click(screen.getByText('4.2.0'));
        fireEvent.click(screen.getByText('Something else…'));
        fireEvent.change(screen.getByPlaceholderText('Something else…'), {target: {value: 'x'}});
        fireEvent.click(screen.getByText('Answer'));

        expect(mockDoAskUserResponse).toHaveBeenCalledWith(POST_ID, 'agentbot', {
            action: 'answer',
            selected: ['4.2.0'],
            free_form: 'x',
        });

        // The local resolved snapshot previews with the server's em-dash rule.
        expect(await screen.findByText('4.2.0 — x')).not.toBeNull();
    });

    test('Answer is disabled until a selection exists', () => {
        renderPost();

        expect((screen.getByText('Answer').closest('button') as HTMLButtonElement).disabled).toBe(true);
    });

    test('free-form alongside options submits the typed text', async () => {
        renderPost();

        fireEvent.click(screen.getByText('Something else…'));
        fireEvent.change(screen.getByPlaceholderText('Something else…'), {target: {value: 'It was a config change'}});
        fireEvent.click(screen.getByText('Answer'));

        expect(mockDoAskUserResponse).toHaveBeenCalledWith(POST_ID, 'agentbot', {
            action: 'answer',
            selected: [],
            free_form: 'It was a config change',
        });
        expect(await screen.findByText('Answered')).not.toBeNull();
    });

    test('free-form-only question renders a textarea and submits trimmed text', async () => {
        renderPost({ask_user_options: []});

        const answerButton = () => screen.getByText('Answer').closest('button') as HTMLButtonElement;
        expect(answerButton().disabled).toBe(true);

        fireEvent.change(screen.getByPlaceholderText('Type your answer…'), {target: {value: '   '}});
        expect(answerButton().disabled).toBe(true);

        fireEvent.change(screen.getByPlaceholderText('Type your answer…'), {target: {value: '  the 4.2.1 hotfix  '}});
        expect(answerButton().disabled).toBe(false);
        fireEvent.click(answerButton());

        expect(mockDoAskUserResponse).toHaveBeenCalledWith(POST_ID, 'agentbot', {
            action: 'answer',
            selected: [],
            free_form: 'the 4.2.1 hotfix',
        });
        expect(await screen.findByText('Answered')).not.toBeNull();
    });

    test('renders no free-form input when allow_free_form is false', () => {
        renderPost({ask_user_allow_free_form: false});

        expect(screen.queryByText('Something else…')).toBeNull();
        expect(screen.queryByRole('textbox')).toBeNull();
    });

    test('decline submits without a selection', async () => {
        mockDoAskUserResponse.mockResolvedValue({status: 'declined'});
        renderPost();

        fireEvent.click(screen.getByText('Decline'));

        expect(mockDoAskUserResponse).toHaveBeenCalledWith(POST_ID, 'agentbot', {
            action: 'decline',
            selected: [],
            free_form: '',
        });
        expect(await screen.findByText('You declined to answer')).not.toBeNull();
    });

    test('submission is disabled until the bot profile is available', async () => {
        // Only the requester profile is cached; the card bot (the post
        // author) is unknown, so the botUsername query param can't be built.
        mockUseSelector.mockImplementation((selector) => selector(stateFixture({
            profiles: {[REQUESTER_ID]: {id: REQUESTER_ID, username: 'jane'}},
        })));
        renderPost();

        fireEvent.click(screen.getByText('4.2.0'));

        expect((screen.getByText('Answer').closest('button') as HTMLButtonElement).disabled).toBe(true);
        expect((screen.getByText('Decline').closest('button') as HTMLButtonElement).disabled).toBe(true);

        fireEvent.click(screen.getByText('Answer'));
        fireEvent.click(screen.getByText('Decline'));
        expect(mockDoAskUserResponse).not.toHaveBeenCalled();

        // The card fetches the missing bot profile so submission can unlock.
        await waitFor(() => {
            expect(mockGetProfilesByIds).toHaveBeenCalledWith([BOT_ID]);
        });
    });

    test('shows the submitting state and prevents a second submission', async () => {
        mockDoAskUserResponse.mockImplementation(() => new Promise(() => {
            // Never resolves — keeps the card in the submitting phase.
        }));
        renderPost();

        fireEvent.click(screen.getByText('4.2.0'));
        fireEvent.click(screen.getByText('Answer'));

        expect(await screen.findByText('Submitting…')).not.toBeNull();
        expect(screen.queryByText('Answer')).toBeNull();
        expect(screen.queryByText('Decline')).toBeNull();

        // Option rows are inert while submitting; nothing can re-trigger.
        fireEvent.click(screen.getByText('4.2.1'));
        expect(mockDoAskUserResponse).toHaveBeenCalledTimes(1);
    });

    test('409 conflict shows the neutral no-longer-needed state and resolves from patched props', async () => {
        mockDoAskUserResponse.mockRejectedValue({status_code: 409});
        const {rerender} = renderPost();

        fireEvent.click(screen.getByText('4.2.0'));
        fireEvent.click(screen.getByText('Answer'));

        // The question was resolved elsewhere (answer race or initiator
        // cancel) — a neutral line, never the red error copy (V2-C4).
        expect(await screen.findByText('This question is no longer needed.')).not.toBeNull();
        expect(screen.queryByText('Failed to submit your response. Please try again.')).toBeNull();

        // Conflict means someone else resolved the question; controls are dead.
        expect(screen.queryByText('Answer')).toBeNull();

        // The server patch arrives as new props via the post-edited event;
        // props win over local state and the neutral line clears.
        rerender(buildPostElement({
            ask_user_status: 'answered',
            ask_user_answer_preview: '4.2.1',
            ask_user_answered_at: 1712345678901,
        }));

        expect(screen.getByText('Answered')).not.toBeNull();
        expect(screen.getByText('4.2.1')).not.toBeNull();
        expect(screen.queryByText('This question is no longer needed.')).toBeNull();
    });

    test('a 409 raced by a cancel settles into the canceled terminal state from props', async () => {
        mockDoAskUserResponse.mockRejectedValue({status_code: 409});
        const {rerender} = renderPost({...V2_BASE});

        fireEvent.click(screen.getByText('4.2.0'));
        fireEvent.click(screen.getByText('Answer'));

        expect(await screen.findByText('This question is no longer needed.')).not.toBeNull();

        // The canceled card patch arrives; props win and the terminal
        // canceled rendering takes over (same copy, no controls).
        rerender(buildPostElement({...V2_BASE, ask_user_status: 'canceled'}));

        expect(screen.getByText('This question is no longer needed.')).not.toBeNull();
        expect(screen.queryByText('Answer')).toBeNull();
        expect(screen.queryByText('4.2.0')).toBeNull();
    });

    test('generic failure shows the retry error and keeps controls enabled', async () => {
        mockDoAskUserResponse.mockRejectedValue({status_code: 500});
        renderPost();

        fireEvent.click(screen.getByText('4.2.0'));
        fireEvent.click(screen.getByText('Answer'));

        expect(await screen.findByText('Failed to submit your response. Please try again.')).not.toBeNull();
        expect((screen.getByText('Answer').closest('button') as HTMLButtonElement).disabled).toBe(false);
    });
});

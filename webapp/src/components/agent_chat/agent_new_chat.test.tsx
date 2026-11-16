// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import manifest from '@/manifest';
import {renderCustomPrompt, createPost} from '@/client';
import {fetchCustomPrompts, fetchPinnedPromptIds} from '@/redux';

import {lastAdvancedTextEditorProps} from '../../../tests/fake_advanced_text_editor';
import {usePinnedSkillsLayoutMock} from '../../../tests/pinned_skills_layout_mock';

import AgentNewChat, {NEW_CHAT_GREETINGS} from './agent_new_chat';

jest.mock('@/client', () => ({
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
    renderCustomPrompt: jest.fn(),
    createPost: jest.fn(),
}));

// The data fetches are covered elsewhere; the state the tests seed is what renders.
jest.mock('@/redux', () => ({
    fetchCustomPrompts: jest.fn(() => ({type: 'noop'})),
    fetchPinnedPromptIds: jest.fn(() => ({type: 'noop'})),
}));

jest.mock('@/mm_webapp', () => ({
    AdvancedTextEditor: jest.requireActual('../../../tests/fake_advanced_text_editor').FakeAdvancedTextEditor,
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
        ),
    };
    return {...actual, useIntl: () => intl};
});

const makePrompt = (id: string) => ({id, name: `Prompt ${id}`, description: '', template: '', creator_id: 'me', is_shared: false, created_at: 0, updated_at: 0, deleted_at: 0});

// null is the store's "not fetched yet".
const makeStore = (prompts: ReturnType<typeof makePrompt>[] | null = [], pinnedPromptIds: string[] | null = []) => createStore(() => ({
    entities: {users: {currentUserId: 'me'}},
    [`plugins-${manifest.id}`]: {customPrompts: prompts, pinnedPromptIds},
}));

let store = makeStore();

const bot = {
    id: 'bot1',
    displayName: 'Matty',
    username: 'matty',
    lastIconUpdate: 0,
    dmChannelID: 'dm1',
    channelAccessLevel: 0,
    channelIDs: [],
    userAccessLevel: 0,
    userIDs: [],
    teamIDs: [],
    enabledMCPTools: [],
    autoEnableNewMCPTools: false,
} as any;

const ui = (layout: 'page' | 'panel' = 'panel', onCreated = jest.fn()) => (
    <Provider store={store}>
        <AgentNewChat
            bot={bot}
            channelId='dm1'
            layout={layout}
            onCreated={onCreated}
        />
    </Provider>
);

const deferred = <T, >() => {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((res) => {
        resolve = res;
    });
    return {promise, resolve};
};

const text = (d: {defaultMessage?: unknown}) => d.defaultMessage as string;
const shown = () => ({
    title: screen.getByRole('heading', {level: 2}).textContent,
    subtitle: screen.getByRole('heading', {level: 2}).nextElementSibling?.textContent,
});

describe('AgentNewChat', () => {
    // The pinned skills render nothing without a measured width; jsdom has no layout.
    usePinnedSkillsLayoutMock();

    beforeEach(() => {
        store = makeStore();
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test.each(NEW_CHAT_GREETINGS.map((greeting, index) => ({index, greeting})))(
        'random index $index shows its own title and subtitle together',
        ({index, greeting}) => {
            jest.spyOn(Math, 'random').mockReturnValue((index + 0.5) / NEW_CHAT_GREETINGS.length);
            render(ui());

            const {title, subtitle} = shown();
            expect(title).toBe(text(greeting.title));
            expect(subtitle).toBe(text(greeting.subtitle));

            // Never mixed with another pair's subtitle.
            NEW_CHAT_GREETINGS.forEach((other, j) => {
                if (j !== index) {
                    expect(subtitle).not.toBe(text(other.subtitle));
                }
            });
        },
    );

    test('the pair is stable across re-renders', () => {
        const random = jest.spyOn(Math, 'random').mockReturnValue(0.99);
        const {rerender} = render(ui());
        const first = shown();

        random.mockReturnValue(0);
        rerender(ui());
        expect(shown()).toEqual(first);
        expect(first.title).toBe(text(NEW_CHAT_GREETINGS[NEW_CHAT_GREETINGS.length - 1].title));
    });

    test.each([
        {layout: 'page' as const, location: 'CENTER'},
        {layout: 'panel' as const, location: 'RHS_COMMENT'},
    ])('in the $layout layout, the host composer posts a root post to the agent\'s DM', ({layout, location}) => {
        render(ui(layout));
        expect(screen.getByTestId('agent-chat-new-composer')).toBeTruthy();
        expect(lastAdvancedTextEditorProps()).toEqual(expect.objectContaining({location, channelId: 'dm1', rootId: '', placeholder: 'Message Matty'}));
    });

    describe('pinned skills', () => {
        const chips = () => screen.queryAllByRole('button').filter((b) => b.textContent?.startsWith('Prompt '));

        test('shows only pinned prompts, in pin order', () => {
            store = makeStore(['1', '2', '3', '4'].map(makePrompt), ['3', '1']);
            render(ui());
            expect(chips().map((b) => b.textContent)).toEqual(['Prompt 3', 'Prompt 1']);
        });

        test.each([
            {name: 'there are no custom prompts', prompts: [] as string[], pinned: [] as string[]},
            {name: 'no prompt is pinned', prompts: ['1', '2'], pinned: [] as string[]},
        ])('shows nothing when $name', ({prompts, pinned}) => {
            store = makeStore(prompts.map(makePrompt), pinned);
            render(ui());
            expect(screen.queryByTestId('agent-chat-pinned-skills')).toBeNull();
        });

        test('ignores a pin whose prompt is gone', () => {
            store = makeStore(['1', '2'].map(makePrompt), ['gone', '2']);
            render(ui());
            expect(chips().map((b) => b.textContent)).toEqual(['Prompt 2']);
        });

        test.each([
            {name: 'fetches the prompts and the pins when they have not loaded', loaded: false, calls: 1},
            {name: 'does not refetch prompts and pins that have loaded', loaded: true, calls: 0},
        ])('$name', ({loaded, calls}) => {
            (fetchCustomPrompts as jest.Mock).mockClear();
            (fetchPinnedPromptIds as jest.Mock).mockClear();
            store = loaded ? makeStore([makePrompt('1')], ['1']) : makeStore(null, null);
            render(ui());
            expect(fetchCustomPrompts).toHaveBeenCalledTimes(calls);
            expect(fetchPinnedPromptIds).toHaveBeenCalledTimes(calls);
        });

        test('two clicks before React re-renders post the prompt once', async () => {
            (renderCustomPrompt as jest.Mock).mockResolvedValue({rendered: 'rendered text'});
            (createPost as jest.Mock).mockReset().mockResolvedValue({id: 'post1'});
            store = makeStore([makePrompt('1')], ['1']);
            const onCreated = jest.fn();
            render(ui('page', onCreated));
            const button = screen.getByRole('button', {name: 'Prompt 1'});

            // Both land in one batch, so neither render has disabled the button for the other.
            act(() => {
                button.click();
                button.click();
            });

            await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));
            expect(createPost).toHaveBeenCalledTimes(1);
        });

        test('clicking a prompt posts its rendered text to the agent DM and opens the chat', async () => {
            (renderCustomPrompt as jest.Mock).mockResolvedValue({rendered: 'rendered text'});
            (createPost as jest.Mock).mockResolvedValue({id: 'post1'});
            store = makeStore([makePrompt('1')], ['1']);
            const onCreated = jest.fn();
            render(ui('page', onCreated));

            fireEvent.click(screen.getByRole('button', {name: 'Prompt 1'}));

            await waitFor(() => expect(onCreated).toHaveBeenCalledWith('post1'));
            expect(renderCustomPrompt).toHaveBeenCalledWith('1', 'dm1', 'bot1');
            expect(createPost).toHaveBeenCalledWith({channel_id: 'dm1', message: 'rendered text', root_id: ''});
        });

        test('a failed run posts nothing, says so and re-enables the prompts; the next attempt clears the message', async () => {
            jest.spyOn(console, 'error').mockImplementation(() => 0);
            (renderCustomPrompt as jest.Mock).mockRejectedValueOnce(new Error('nope'));
            (createPost as jest.Mock).mockReset().mockResolvedValue({id: 'post1'});
            store = makeStore([makePrompt('1')], ['1']);
            const onCreated = jest.fn();
            render(ui('page', onCreated));

            fireEvent.click(screen.getByRole('button', {name: 'Prompt 1'}));
            expect((await screen.findByRole('alert')).textContent).toBe('Couldn\'t run that skill. Please try again.');
            expect((screen.getByRole('button', {name: 'Prompt 1'}) as HTMLButtonElement).disabled).toBe(false);
            expect(createPost).not.toHaveBeenCalled();
            expect(onCreated).not.toHaveBeenCalled();

            (renderCustomPrompt as jest.Mock).mockResolvedValue({rendered: 'rendered text'});
            fireEvent.click(screen.getByRole('button', {name: 'Prompt 1'}));
            await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
            await waitFor(() => expect(onCreated).toHaveBeenCalledWith('post1'));
        });

        test('a post that comes back without an id is reported as a failure', async () => {
            (renderCustomPrompt as jest.Mock).mockResolvedValue({rendered: 'rendered text'});
            (createPost as jest.Mock).mockReset().mockResolvedValue({});
            store = makeStore([makePrompt('1')], ['1']);
            const onCreated = jest.fn();
            render(ui('page', onCreated));

            fireEvent.click(screen.getByRole('button', {name: 'Prompt 1'}));

            expect(await screen.findByRole('alert')).toBeTruthy();
            expect(onCreated).not.toHaveBeenCalled();
        });

        test('the composer is inert while a pinned skill runs and usable again after a failure', async () => {
            jest.spyOn(console, 'error').mockImplementation(() => 0);
            const render1 = deferred<{rendered: string}>();
            (renderCustomPrompt as jest.Mock).mockReturnValue(render1.promise);
            store = makeStore([makePrompt('1')], ['1']);
            render(ui());
            const composer = screen.getByTestId('agent-chat-new-composer');
            expect(composer.hasAttribute('inert')).toBe(false);

            fireEvent.click(screen.getByRole('button', {name: 'Prompt 1'}));
            expect(composer.hasAttribute('inert')).toBe(true);

            await act(async () => {
                render1.resolve(Promise.reject(new Error('nope')) as never);
            });
            await waitFor(() => expect(composer.hasAttribute('inert')).toBe(false));
        });

        test('a pinned skill does not post a second first message once the composer has sent one', async () => {
            const render1 = deferred<{rendered: string}>();
            (renderCustomPrompt as jest.Mock).mockReturnValue(render1.promise);
            (createPost as jest.Mock).mockReset().mockResolvedValue({id: 'skill-post'});
            store = makeStore([makePrompt('1')], ['1']);
            const onCreated = jest.fn();
            render(ui('page', onCreated));

            fireEvent.click(screen.getByRole('button', {name: 'Prompt 1'}));

            // The composer sends the chat's first message while the prompt is still rendering.
            act(() => lastAdvancedTextEditorProps().afterSubmit!({created: {id: 'composer-post'}}));
            await act(async () => {
                render1.resolve({rendered: 'rendered text'});
            });

            expect(createPost).not.toHaveBeenCalled();
            expect(onCreated).toHaveBeenCalledTimes(1);
            expect(onCreated).toHaveBeenCalledWith('composer-post');
        });

        test('a pinned skill does not post after the view is gone', async () => {
            const render1 = deferred<{rendered: string}>();
            (renderCustomPrompt as jest.Mock).mockReturnValue(render1.promise);
            (createPost as jest.Mock).mockReset().mockResolvedValue({id: 'skill-post'});
            store = makeStore([makePrompt('1')], ['1']);
            const onCreated = jest.fn();
            const {unmount} = render(ui('page', onCreated));

            fireEvent.click(screen.getByRole('button', {name: 'Prompt 1'}));
            unmount();
            await act(async () => {
                render1.resolve({rendered: 'rendered text'});
            });

            expect(createPost).not.toHaveBeenCalled();
            expect(onCreated).not.toHaveBeenCalled();
        });

        test('a pinned skill that posts after the view is gone does not navigate', async () => {
            (renderCustomPrompt as jest.Mock).mockResolvedValue({rendered: 'rendered text'});
            const post = deferred<{id: string}>();
            (createPost as jest.Mock).mockReset().mockReturnValue(post.promise);
            store = makeStore([makePrompt('1')], ['1']);
            const onCreated = jest.fn();
            const {unmount} = render(ui('page', onCreated));

            fireEvent.click(screen.getByRole('button', {name: 'Prompt 1'}));
            await waitFor(() => expect(createPost).toHaveBeenCalledTimes(1));
            unmount();
            await act(async () => {
                post.resolve({id: 'skill-post'});
            });

            expect(onCreated).not.toHaveBeenCalled();
        });
    });
});

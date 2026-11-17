// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import manifest from '@/manifest';
import {doStopGenerating, getAgentsBridgeStatus, renderCustomPrompt, rewriteMessage} from '@/client';

import {clearGeneratingPosts, noteLivePost} from './use_thread_generating';
import AgentComposerChrome from './agent_composer_chrome';

jest.mock('@/client', () => ({
    doStopGenerating: jest.fn(() => Promise.resolve()),
    renderCustomPrompt: jest.fn(),
    rewriteMessage: jest.fn(),
    getAgentsBridgeStatus: jest.fn(() => Promise.resolve({available: true})),
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_match, key) => values?.[key] ?? '')
        ),
    };
    return {...actual, useIntl: () => intl};
});

const prompt = {id: 'skill1', name: 'Summarize thread', description: '', template: '', creator_id: 'me', is_shared: false, created_at: 0, updated_at: 0, deleted_at: 0};

const post = (id: string, rootId: string) => ({
    id,
    channel_id: 'dm1',
    root_id: rootId,
    user_id: 'bot1',
    props: {llm_requester_user_id: 'me'},
});

const store = createStore(() => ({
    entities: {
        users: {currentUserId: 'me'},
        posts: {posts: {rootA: post('rootA', ''), reply1: post('reply1', 'rootA')}},
    },
    [`plugins-${manifest.id}`]: {
        customPrompts: [prompt],
        pinnedPromptIds: ['skill1'],
        bots: [{id: 'bot1', dmChannelID: 'dm1'}],
    },
}));

const host = ({
    bar = false,
    attributesDisabled = true,
    sendDisabled = false,
}: {bar?: boolean; attributesDisabled?: boolean; sendDisabled?: boolean} = {}) => (
    <div className='AdvancedTextEditor'>
        <div className='AdvancedTextEditor__body'>
            <textarea defaultValue='hello world'/>
            {bar && <div data-testid='formattingBarContainer'>{'bar'}</div>}
            <button
                id='fileUploadButton'
                aria-label='file'
            />
            <button
                id='emojiPickerButton'
                aria-label='emoji'
            />
            <button
                id='postAttributesButton'
                aria-label='attributes'
                disabled={attributesDisabled}
            />
            <button
                id='toggleFormattingBarButton'
                aria-label='format'
            />
            <button
                data-testid='SendMessageButton'
                aria-label='send'
                disabled={sendDisabled}
            />
        </div>
    </div>
);

const renderChrome = (ui: React.ReactNode, rootId = 'rootA') => render(
    <Provider store={store}>
        <AgentComposerChrome
            rootId={rootId}
            channelId='dm1'
            botId='bot1'
        >
            {ui}
        </AgentComposerChrome>
    </Provider>,
);

const openMenu = () => {
    fireEvent.click(screen.getByTestId('agent-composer-add'));
};

// The agent mode hides the host controls, so their accessible names are not how tests find them.
const hostControl = (id: string) => {
    const control = document.getElementById(id);
    if (!control) {
        throw new Error(`missing host control ${id}`);
    }
    return control;
};

describe('AgentComposerChrome', () => {
    beforeEach(() => {
        act(() => clearGeneratingPosts());
        jest.clearAllMocks();
    });

    test('the formatting bar starts closed and the menu opens it without the shared preference', () => {
        const {container} = renderChrome(host({bar: true}));
        const composer = container.querySelector('[data-agent-composer]') as HTMLElement;
        const toggle = hostControl('toggleFormattingBarButton');
        const click = jest.spyOn(toggle, 'click');
        expect(composer.getAttribute('data-agent-formatting')).toBe('closed');

        openMenu();
        fireEvent.click(screen.getByRole('menuitemcheckbox', {name: 'Show formatting'}));

        expect(composer.getAttribute('data-agent-formatting')).toBe('open');
        expect(click).not.toHaveBeenCalled();
    });

    test('opening formatting clicks the host toggle only when the host has not rendered the bar, and closing clicks it back', () => {
        renderChrome(host({bar: false}));
        const toggle = hostControl('toggleFormattingBarButton');
        const click = jest.spyOn(toggle, 'click');

        openMenu();
        fireEvent.click(screen.getByRole('menuitemcheckbox', {name: 'Show formatting'}));
        expect(click).toHaveBeenCalledTimes(1);

        openMenu();
        fireEvent.click(screen.getByRole('menuitemcheckbox', {name: 'Hide formatting'}));
        expect(click).toHaveBeenCalledTimes(2);
    });

    test.each([
        {name: 'upload', item: 'Upload files', hostId: 'fileUploadButton'},
        {name: 'emoji', item: 'Add emoji', hostId: 'emojiPickerButton'},
    ])('$name clicks the host control', ({item, hostId}) => {
        renderChrome(host());
        const control = hostControl(hostId);
        const click = jest.spyOn(control, 'click');
        openMenu();
        fireEvent.click(screen.getByRole('menuitem', {name: item}));
        expect(click).toHaveBeenCalledTimes(1);
    });

    test('add attributes stays disabled until the host button can be used', () => {
        const {rerender} = renderChrome(host({attributesDisabled: true}));
        openMenu();
        expect((screen.getByRole('menuitem', {name: 'Add attributes'}) as HTMLButtonElement).disabled).toBe(true);
        fireEvent.click(screen.getByTestId('agent-composer-add'));

        rerender(
            <Provider store={store}>
                <AgentComposerChrome
                    rootId='rootA'
                    channelId='dm1'
                    botId='bot1'
                >
                    {host({attributesDisabled: false})}
                </AgentComposerChrome>
            </Provider>,
        );
        openMenu();
        const control = hostControl('postAttributesButton');
        const click = jest.spyOn(control, 'click');
        fireEvent.click(screen.getByRole('menuitem', {name: 'Add attributes'}));
        expect(click).toHaveBeenCalledTimes(1);
    });

    test('a skill replaces the draft', async () => {
        (renderCustomPrompt as jest.Mock).mockResolvedValue({rendered: 'rendered skill'});
        renderChrome(host());
        openMenu();
        fireEvent.click(screen.getByRole('menuitem', {name: 'Skills'}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Summarize thread'}));

        await waitFor(() => expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('rendered skill'));
        expect(renderCustomPrompt).toHaveBeenCalledWith('skill1', 'dm1', 'bot1');
    });

    test('rewrite replaces the selection and holds send until it finishes', async () => {
        let resolveRewrite: (value: string) => void = () => {
            throw new Error('rewrite was not started');
        };
        (rewriteMessage as jest.Mock).mockReturnValue(new Promise((resolve) => {
            resolveRewrite = resolve;
        }));
        renderChrome(host());
        const area = screen.getByRole('textbox') as HTMLTextAreaElement;
        area.setSelectionRange(0, 5);
        const send = screen.getByRole('button', {name: 'send'}) as HTMLButtonElement;

        openMenu();
        fireEvent.click(screen.getByRole('menuitem', {name: 'Rewrite'}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Shorten'}));

        await waitFor(() => expect(send.disabled).toBe(true));
        resolveRewrite('short');
        await waitFor(() => expect(area.value).toBe('short world'));
        await waitFor(() => expect(send.disabled).toBe(false));
        expect(rewriteMessage).toHaveBeenCalledWith('hello', 'shorten', 'bot1');
    });

    test('send becomes stop while this thread generates, and a failed stop brings it back', async () => {
        (doStopGenerating as jest.Mock).mockRejectedValue(new Error('no'));
        act(() => noteLivePost('reply1'));
        renderChrome(host());

        expect(screen.getByTestId('agent-composer').getAttribute('data-agent-generating')).toBe('true');
        fireEvent.click(screen.getByTestId('agent-composer-stop'));

        expect(doStopGenerating).toHaveBeenCalledWith('reply1');
        expect((await screen.findByText('Couldn\'t stop generating. Try again.'))).toBeTruthy();
        expect(screen.getByTestId('agent-composer-stop')).toBeTruthy();
    });

    test('a new chat has no stop', () => {
        act(() => noteLivePost('reply1'));
        renderChrome(host(), '');
        expect(screen.queryByTestId('agent-composer-stop')).toBeNull();
    });

    test('rewrite stays out of the menu when the server cannot rewrite', async () => {
        (getAgentsBridgeStatus as jest.Mock).mockResolvedValue({available: false});
        renderChrome(host());
        openMenu();
        fireEvent.click(screen.getByRole('menuitem', {name: 'Rewrite'}));
        expect((await screen.findByRole('status')).textContent).toBe('Rewrite isn\'t available');
        expect(screen.queryByRole('menuitem', {name: 'Shorten'})).toBeNull();
    });
});

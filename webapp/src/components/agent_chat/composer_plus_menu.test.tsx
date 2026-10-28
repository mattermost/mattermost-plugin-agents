// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen, waitFor, within} from '@testing-library/react';

import manifest from '@/manifest';

import {bot, composerText, openMenu, openSubmenu, renderMenu, trigger, waitForMenuClosed} from './composer_plus_menu_test_harness';

const mockClient = {
    getUserToolPreferences: jest.fn(),
    getUserMCPTools: jest.fn(),
    refreshUserMCPTools: jest.fn(),
    updateUserToolPreferences: jest.fn(),
    disconnectMCPOAuth: jest.fn(),
    getCustomPrompts: jest.fn(),
    getCustomPromptPins: jest.fn(),
    renderCustomPrompt: jest.fn(),
    rewriteMessage: jest.fn(),
    getAgentsBridgeStatus: jest.fn(),
};
jest.mock('@/client', () => {
    const wrap = (name: string) => (...args: unknown[]) => (mockClient as Record<string, jest.Mock>)[name](...args);
    return Object.fromEntries([
        'getUserToolPreferences', 'getUserMCPTools', 'refreshUserMCPTools', 'updateUserToolPreferences', 'disconnectMCPOAuth',
        'getCustomPrompts', 'getCustomPromptPins', 'renderCustomPrompt', 'rewriteMessage', 'getAgentsBridgeStatus',
    ].map((name) => [name, wrap(name)]));
});

jest.mock('@/license', () => ({
    useIsLicensedFor: () => true,
    licenseAllows: () => false,
}));

jest.mock('@/hooks/use_mcp_connection_events', () => ({
    useMCPConnectionEvents: jest.fn(),
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
        ),
    };
    return {...actual, useIntl: () => intl, FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage};
});

const prompt = (id: string, name: string) => ({id, name, creator_id: 'user1', description: '', template: '', is_shared: false, created_at: 0, updated_at: 0, deleted_at: 0});

const mockPush = jest.fn();
const setPath = (pathname: string) => {
    (window as any).WebappUtils = {browserHistory: {push: mockPush, location: {pathname}}};
};

const deferred = <T, >() => {
    let resolve!: (value: T) => void;
    let reject!: (e: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
        resolve = res;
        reject = rej;
    });
    return {promise, resolve, reject};
};

beforeEach(() => {
    jest.clearAllMocks();
    Object.values(mockClient).forEach((fn) => fn.mockReset());
    setPath('/team/channels/town-square');
    mockClient.getUserToolPreferences.mockResolvedValue({disabled_servers: []});
    mockClient.getUserMCPTools.mockResolvedValue({servers: []});
    mockClient.getCustomPrompts.mockResolvedValue([]);
    mockClient.getCustomPromptPins.mockResolvedValue([]);
    mockClient.getAgentsBridgeStatus.mockResolvedValue({available: true});
    jest.spyOn(console, 'error').mockImplementation(() => null);
});

afterEach(() => {
    jest.restoreAllMocks();
});

const topItems = () => {
    const menu = screen.getByRole('menu', {name: 'Add to message'});
    return within(menu).getAllByRole('menuitem').filter((el) => el.closest('[role="menu"]') === menu).map((el) => el.textContent);
};

describe('ComposerPlusMenu trigger and items', () => {
    test('the "+" is a menu button that opens and closes the menu', async () => {
        renderMenu();
        expect(screen.getByRole('button', {name: 'Add to message'})).toBe(trigger());
        expect(trigger().getAttribute('aria-haspopup')).toBe('menu');
        expect(trigger().getAttribute('aria-expanded')).toBe('false');

        await openMenu();
        expect(trigger().getAttribute('aria-expanded')).toBe('true');

        fireEvent.click(trigger());
        await waitForMenuClosed();
    });

    test('disabled disables the "+"', () => {
        renderMenu({disabled: true});
        expect((trigger() as HTMLButtonElement).disabled).toBe(true);
    });

    test.each([
        {name: 'a new chat with a user-auth agent', props: {}, items: ['Upload files', 'Tools', 'Custom prompts', 'AI Rewrite']},
        {name: 'uploads unavailable', props: {uploadEnabled: false}, items: ['Tools', 'Custom prompts', 'AI Rewrite']},
        {name: 'an existing thread (no Tools, as the header popover)', props: {rootId: 'root1'}, items: ['Upload files', 'Custom prompts', 'AI Rewrite']},
        {name: 'a service account agent', props: {bot: {...bot, useServiceAccountAuth: true}}, items: ['Upload files', 'Custom prompts', 'AI Rewrite']},
        {name: 'no agent', props: {bot: null}, items: ['Upload files', 'Custom prompts']},
    ])('shows the right items for $name', async ({props, items}) => {
        renderMenu(props);
        await openMenu();
        await waitFor(() => expect(topItems()).toEqual(items));
    });

    test('leaves AI Rewrite out when the server cannot reach the agents bridge', async () => {
        mockClient.getAgentsBridgeStatus.mockResolvedValue({available: false, reason: 'plugin_disabled'});
        renderMenu();
        await openMenu();
        await waitFor(() => expect(topItems()).toEqual(['Upload files', 'Tools', 'Custom prompts']));
    });

    test('shows AI Rewrite as unavailable when the availability check fails', async () => {
        mockClient.getAgentsBridgeStatus.mockRejectedValue(new Error('boom'));
        renderMenu();
        await openMenu();
        const row = await screen.findByRole('menuitem', {name: 'AI Rewrite Unavailable'});
        expect((row as HTMLButtonElement).disabled).toBe(true);
    });

    test('Upload files opens the picker and closes the menu', async () => {
        const {onUploadFiles} = renderMenu();
        await openMenu();
        fireEvent.click(screen.getByRole('menuitem', {name: 'Upload files'}));
        expect(onUploadFiles).toHaveBeenCalledTimes(1);
        await waitForMenuClosed();
    });

    test('reopening right after an item ran still moves focus onto the first item', async () => {
        renderMenu();
        await openMenu();
        fireEvent.click(screen.getByRole('menuitem', {name: 'Upload files'}));
        expect(document.activeElement).toBe(trigger());
        await waitForMenuClosed();

        fireEvent.click(trigger());
        await waitFor(() => expect(document.activeElement?.textContent).toBe('Upload files'));
    });
});

describe('ComposerPlusMenu keyboard', () => {
    test('ArrowDown opens the menu on its first item; arrows move, Escape closes and returns focus', async () => {
        renderMenu();
        trigger().focus();
        fireEvent.keyDown(trigger(), {key: 'ArrowDown'});
        await waitFor(() => expect(document.activeElement?.textContent).toBe('Upload files'));

        fireEvent.keyDown(document.activeElement as Element, {key: 'ArrowDown'});
        expect(document.activeElement?.textContent).toBe('Tools');
        fireEvent.keyDown(document.activeElement as Element, {key: 'End'});
        expect(document.activeElement?.textContent).toBe('AI Rewrite');
        fireEvent.keyDown(document.activeElement as Element, {key: 'ArrowDown'});
        expect(document.activeElement?.textContent).toBe('Upload files');
        fireEvent.keyDown(document.activeElement as Element, {key: 'ArrowUp'});
        expect(document.activeElement?.textContent).toBe('AI Rewrite');

        fireEvent.keyDown(document.activeElement as Element, {key: 'Escape'});
        expect(document.activeElement).toBe(trigger());
        await waitForMenuClosed();
    });

    test('ArrowRight opens a submenu on its first item; ArrowLeft and Escape close only the submenu', async () => {
        mockClient.getCustomPrompts.mockResolvedValue([prompt('p1', 'Summary'), prompt('p2', 'Standup')]);
        renderMenu();
        await openMenu();
        const row = screen.getByRole('menuitem', {name: 'Custom prompts'});
        row.focus();

        for (const closeKey of ['ArrowLeft', 'Escape']) {
            fireEvent.keyDown(row, {key: 'ArrowRight'});
            expect(row.getAttribute('aria-expanded')).toBe('true');
            // eslint-disable-next-line no-await-in-loop
            const submenu = await screen.findByRole('menu', {name: 'Custom prompts'});
            // eslint-disable-next-line no-await-in-loop
            await waitFor(() => expect(document.activeElement?.textContent).toBe('Summary'));
            fireEvent.keyDown(document.activeElement as Element, {key: 'ArrowDown'});
            expect(document.activeElement?.textContent).toBe('Standup');
            expect(submenu.contains(document.activeElement)).toBe(true);

            fireEvent.keyDown(document.activeElement as Element, {key: closeKey});
            expect(row.getAttribute('aria-expanded')).toBe('false');
            expect(document.activeElement).toBe(row);
            expect(screen.getByRole('menu', {name: 'Add to message'})).toBeTruthy();
        }
    });

    test('hovering a row opens its submenu and hovering another switches it', async () => {
        renderMenu();
        await openMenu();
        fireEvent.mouseEnter(screen.getByRole('menuitem', {name: 'Custom prompts'}));
        expect(await screen.findByRole('menu', {name: 'Custom prompts'})).toBeTruthy();
        fireEvent.mouseEnter(screen.getByRole('menuitem', {name: 'Upload files'}));
        await waitFor(() => expect(screen.queryByRole('menu', {name: 'Custom prompts'})).toBeNull());
    });
});

describe('ComposerPlusMenu custom prompts', () => {
    test('lists pinned prompts first and replaces the text with the rendered pick', async () => {
        mockClient.getCustomPrompts.mockResolvedValue([prompt('p1', 'Summary'), prompt('p2', 'Standup')]);
        mockClient.getCustomPromptPins.mockResolvedValue(['p2']);
        mockClient.renderCustomPrompt.mockResolvedValue({rendered: 'Write my standup'});
        const {onSetText} = renderMenu({initialText: 'old text'});

        const submenu = await openSubmenu('Custom prompts');
        await waitFor(() => expect(within(submenu).getAllByRole('menuitem').map((el) => el.textContent)).toEqual(['Standup', 'Summary', 'Manage prompts']));
        expect(within(submenu).getByRole('group', {name: 'Pinned'})).toBeTruthy();

        fireEvent.click(within(submenu).getByRole('menuitem', {name: 'Standup'}));
        await waitFor(() => expect(onSetText).toHaveBeenCalledWith('Write my standup'));
        expect(mockClient.renderCustomPrompt).toHaveBeenCalledWith('p2', 'dm1', 'bot1');
        expect(composerText()).toBe('Write my standup');
        await waitForMenuClosed();
    });

    test('outside the agent DM the rendered prompt mentions the agent', async () => {
        mockClient.getCustomPrompts.mockResolvedValue([prompt('p1', 'Summary')]);
        mockClient.renderCustomPrompt.mockResolvedValue({rendered: 'Summarize'});
        const {onSetText} = renderMenu({channelId: 'town-square'});
        const submenu = await openSubmenu('Custom prompts');
        fireEvent.click(await within(submenu).findByRole('menuitem', {name: 'Summary'}));
        await waitFor(() => expect(onSetText).toHaveBeenCalledWith('@matty Summarize'));
        expect(mockClient.renderCustomPrompt).toHaveBeenCalledWith('p1', 'town-square', 'bot1');
    });

    test('a failed render reports an error and leaves the text alone', async () => {
        mockClient.getCustomPrompts.mockResolvedValue([prompt('p1', 'Summary')]);
        mockClient.renderCustomPrompt.mockRejectedValue(new Error('nope'));
        const {onError, onSetText} = renderMenu({initialText: 'keep me'});
        const submenu = await openSubmenu('Custom prompts');
        fireEvent.click(await within(submenu).findByRole('menuitem', {name: 'Summary'}));
        await waitFor(() => expect(onError).toHaveBeenCalledWith('Couldn\'t insert the custom prompt. Try again.'));
        expect(onSetText).not.toHaveBeenCalled();
        expect(composerText()).toBe('keep me');
    });

    test('a failed load shows an error, not an empty list, and Try again reloads', async () => {
        mockClient.getCustomPrompts.mockRejectedValueOnce(new Error('down'));
        renderMenu();
        const submenu = await openSubmenu('Custom prompts');
        expect((await within(submenu).findByRole('alert')).textContent).toBe('Couldn\'t load custom prompts.');
        expect(within(submenu).queryByText('No custom prompts yet')).toBeNull();

        mockClient.getCustomPrompts.mockResolvedValueOnce([prompt('p1', 'Summary')]);
        fireEvent.click(within(submenu).getByRole('menuitem', {name: 'Try again'}));
        expect(await within(submenu).findByRole('menuitem', {name: 'Summary'})).toBeTruthy();
    });

    test('shows a loading line, then the empty state with Manage prompts', async () => {
        const list = deferred<unknown[]>();
        mockClient.getCustomPrompts.mockReturnValue(list.promise);
        renderMenu();
        const submenu = await openSubmenu('Custom prompts');
        expect(within(submenu).getByRole('status').textContent).toBe('Loading custom prompts…');

        await act(async () => list.resolve([]));
        expect(within(submenu).getByRole('status').textContent).toBe('No custom prompts yet');
        expect(within(submenu).getByRole('menuitem', {name: 'Manage prompts'})).toBeTruthy();
    });

    test.each([
        {name: 'inside the Agents product', path: `/plug/${manifest.id}/agents/chat/bot1`},
        {name: 'elsewhere', path: '/team/messages/@matty'},
    ])('Manage prompts $name opens the Custom Prompts modal', async ({path}) => {
        setPath(path);
        const {store} = renderMenu();
        const submenu = await openSubmenu('Custom prompts');
        fireEvent.click(within(submenu).getByRole('menuitem', {name: 'Manage prompts'}));
        await waitForMenuClosed();
        expect(mockPush).not.toHaveBeenCalled();
        expect((store.getState() as any)[`plugins-${manifest.id}`].showCustomPromptsModal).toBe(true);
    });
});

describe('ComposerPlusMenu AI Rewrite', () => {
    const actionLabels = ['Shorten', 'Elaborate', 'Improve writing', 'Fix spelling and grammar', 'Simplify', 'Summarize'];

    test('with no text the rewrite actions are disabled', async () => {
        renderMenu({initialText: '   '});
        const submenu = await openSubmenu('AI Rewrite');
        const actions = within(submenu).getAllByRole('menuitem');
        expect(actions.map((el) => el.textContent)).toEqual(actionLabels);
        expect(actions.every((el) => (el as HTMLButtonElement).disabled)).toBe(true);
        expect(mockClient.rewriteMessage).not.toHaveBeenCalled();
    });

    test('an action rewrites the text for the chat agent and thread, and Discard puts it back', async () => {
        mockClient.rewriteMessage.mockResolvedValue('Hi.');
        const {onSetText, onRewritingChange} = renderMenu({initialText: 'hello there', rootId: 'root1'});
        const submenu = await openSubmenu('AI Rewrite');
        fireEvent.click(within(submenu).getByRole('menuitem', {name: 'Shorten'}));

        await waitFor(() => expect(composerText()).toBe('Hi.'));
        expect(mockClient.rewriteMessage).toHaveBeenCalledWith('bot1', 'hello there', 'shorten', expect.objectContaining({rootId: 'root1'}));
        expect(onRewritingChange).toHaveBeenCalledWith(true);
        expect(onRewritingChange).toHaveBeenLastCalledWith(false);
        await waitForMenuClosed();

        const again = await openSubmenu('AI Rewrite');
        fireEvent.click(within(again).getByRole('menuitem', {name: 'Discard'}));
        expect(onSetText).toHaveBeenLastCalledWith('hello there');
        expect(composerText()).toBe('hello there');
    });

    test('Regenerate reruns the last action on the original text', async () => {
        mockClient.rewriteMessage.mockResolvedValueOnce('Short.').mockResolvedValueOnce('Shorter.');
        renderMenu({initialText: 'a long message'});
        fireEvent.click(within(await openSubmenu('AI Rewrite')).getByRole('menuitem', {name: 'Simplify'}));
        await waitFor(() => expect(composerText()).toBe('Short.'));
        await waitForMenuClosed();

        fireEvent.click(within(await openSubmenu('AI Rewrite')).getByRole('menuitem', {name: 'Regenerate'}));
        await waitFor(() => expect(composerText()).toBe('Shorter.'));
        expect(mockClient.rewriteMessage).toHaveBeenLastCalledWith('bot1', 'a long message', 'simplify', expect.anything());
    });

    test('Enter in the instruction field sends a custom rewrite', async () => {
        mockClient.rewriteMessage.mockResolvedValue('Dear team,');
        renderMenu({initialText: 'hey all'});
        const submenu = await openSubmenu('AI Rewrite');
        const field = within(submenu).getByRole('textbox', {name: 'Ask AI to edit message'});
        fireEvent.change(field, {target: {value: 'make it formal'}});
        fireEvent.keyDown(field, {key: 'Enter'});
        await waitFor(() => expect(composerText()).toBe('Dear team,'));
        expect(mockClient.rewriteMessage).toHaveBeenCalledWith('bot1', 'hey all', 'custom', expect.objectContaining({customPrompt: 'make it formal'}));
    });

    test.each([
        {name: 'the server message', error: Object.assign(new Error('The agent could not rewrite this.'), {status_code: 500}), shown: 'The agent could not rewrite this.'},
        {name: 'a fallback without one', error: new Error(''), shown: 'Couldn\'t rewrite your message. Try again.'},
    ])('a failed rewrite reports $name and keeps the text', async ({error, shown}) => {
        mockClient.rewriteMessage.mockRejectedValue(error);
        const {onError, onSetText} = renderMenu({initialText: 'hello'});
        fireEvent.click(within(await openSubmenu('AI Rewrite')).getByRole('menuitem', {name: 'Elaborate'}));
        await waitFor(() => expect(onError).toHaveBeenCalledWith(shown));
        expect(onSetText).not.toHaveBeenCalled();
        expect(composerText()).toBe('hello');
    });

    test('Stop generating aborts the request and its late response never lands', async () => {
        const pending = deferred<string>();
        mockClient.rewriteMessage.mockReturnValue(pending.promise);
        const {onSetText} = renderMenu({initialText: 'hello'});
        fireEvent.click(within(await openSubmenu('AI Rewrite')).getByRole('menuitem', {name: 'Shorten'}));
        await waitForMenuClosed();

        expect(trigger().getAttribute('aria-busy')).toBe('true');
        const submenu = await openSubmenu('AI Rewrite');
        expect(within(submenu).getByRole('status').textContent).toBe('Rewriting…');
        fireEvent.click(within(submenu).getByRole('menuitem', {name: 'Stop generating'}));
        const signal = mockClient.rewriteMessage.mock.calls[0][3].signal as AbortSignal;
        expect(signal.aborted).toBe(true);

        await act(async () => pending.resolve('too late'));
        expect(onSetText).not.toHaveBeenCalled();
        expect(composerText()).toBe('hello');
    });

    test('unmounting aborts a running rewrite', async () => {
        mockClient.rewriteMessage.mockReturnValue(new Promise(() => null));
        const {unmount} = renderMenu({initialText: 'hello'});
        fireEvent.click(within(await openSubmenu('AI Rewrite')).getByRole('menuitem', {name: 'Shorten'}));
        await waitFor(() => expect(mockClient.rewriteMessage).toHaveBeenCalled());
        unmount();
        expect((mockClient.rewriteMessage.mock.calls[0][3].signal as AbortSignal).aborted).toBe(true);
    });
});

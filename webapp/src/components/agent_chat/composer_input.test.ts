// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {decideEnter, gotoTarget, isSlashCommand, normalizeCommand} from './composer_input';

const key = (overrides: Partial<Parameters<typeof decideEnter>[0]> = {}) => ({
    key: 'Enter',
    shiftKey: false,
    altKey: false,
    ctrlKey: false,
    metaKey: false,
    isComposing: false,
    keyCode: 13,
    ...overrides,
});

const OPEN_BLOCK = '```js\nconst a = 1;';

describe('decideEnter', () => {
    test.each([
        {name: 'Enter sends', e: key(), message: 'hi', prefs: {}, send: true},
        {name: 'Shift+Enter is a newline', e: key({shiftKey: true}), message: 'hi', prefs: {}, send: false},
        {name: 'Alt+Enter is a newline', e: key({altKey: true}), message: 'hi', prefs: {}, send: false},
        {name: 'IME composition never sends', e: key({isComposing: true}), message: 'hi', prefs: {}, send: false},
        {name: 'IME keyCode 229 never sends', e: key({keyCode: 229}), message: 'hi', prefs: {}, send: false},
        {name: 'another key', e: key({key: 'a'}), message: 'hi', prefs: {}, send: false},
        {name: 'Enter inside an open code block is a newline', e: key(), message: OPEN_BLOCK, prefs: {}, send: false},
        {name: 'Ctrl+Enter inside an open code block closes it and sends', e: key({ctrlKey: true}), message: OPEN_BLOCK, prefs: {}, send: true, sent: `${OPEN_BLOCK}\n\`\`\``},
        {name: 'Enter after a closed code block sends', e: key(), message: '```\na\n```', prefs: {}, send: true},
        {name: 'code block rule off: Enter in a block sends', e: key(), message: OPEN_BLOCK, prefs: {codeBlockOnCtrlEnter: false}, send: true},
        {name: 'send on Ctrl+Enter: Enter is a newline', e: key(), message: 'hi', prefs: {sendOnCtrlEnter: true}, send: false},
        {name: 'send on Ctrl+Enter: Ctrl+Enter sends', e: key({ctrlKey: true}), message: 'hi', prefs: {sendOnCtrlEnter: true}, send: true},
        {name: 'send on Ctrl+Enter: Cmd+Enter sends', e: key({metaKey: true}), message: 'hi', prefs: {sendOnCtrlEnter: true}, send: true},
    ])('$name', ({e, message, prefs, send, sent}: {e: ReturnType<typeof key>; message: string; prefs: {sendOnCtrlEnter?: boolean; codeBlockOnCtrlEnter?: boolean}; send: boolean; sent?: string}) => {
        const result = decideEnter(e, {message, caret: message.length, sendOnCtrlEnter: false, codeBlockOnCtrlEnter: true, ...prefs});
        expect(result.send).toBe(send);
        expect(result.message).toBe(sent);
    });
});

describe('slash commands', () => {
    test.each([
        {message: '/echo hi', command: true},
        {message: '//echo hi', command: false},
        {message: 'echo /hi', command: false},
        {message: '', command: false},
    ])('isSlashCommand($message) is $command', ({message, command}) => {
        expect(isSlashCommand(message)).toBe(command);
    });

    test.each([
        {message: '/ECHO  hi there ', normalized: '/echo hi there'},
        {message: '/away', normalized: '/away '},
    ])('normalizeCommand($message)', ({message, normalized}) => {
        expect(normalizeCommand(message)).toBe(normalized);
    });
});

describe('gotoTarget', () => {
    const site = 'https://chat.example.com';
    test.each([
        {location: '/team/channels/town', target: {path: '/team/channels/town'}},
        {location: `${site}/team/pl/abc`, target: {path: '/team/pl/abc'}},
        {location: 'https://elsewhere.example.com/x', target: {url: 'https://elsewhere.example.com/x'}},
        {location: '//evil.example.com', target: null},
        {location: 'data:text/html,hi', target: null},
        {location: '', target: null},
    ])('$location', ({location, target}) => {
        expect(gotoTarget(location, site)).toEqual(target);
    });
});

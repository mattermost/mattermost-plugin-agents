// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, fireEvent, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import ToolArguments from './tool_arguments';
import {JSONValue} from './tool_types';

function renderArgs(args: JSONValue | null | undefined) {
    return render(
        <IntlProvider locale='en'>
            <ToolArguments arguments={args as JSONValue}/>
        </IntlProvider>,
    );
}

describe('ToolArguments', () => {
    test('renders nothing when arguments are null (redacted/absent)', () => {
        const {container} = renderArgs(null);
        expect(container.textContent).toBe('');
    });

    test('renders nothing when arguments are undefined', () => {
        // eslint-disable-next-line no-undefined
        const {container} = renderArgs(undefined);
        expect(container.textContent).toBe('');
    });

    test('shows the no-parameters message for an empty object and no toggles', () => {
        renderArgs({});
        expect(screen.getByText(/No parameters required/)).not.toBeNull();
        expect(screen.queryByText('View raw')).toBeNull();
        expect(screen.queryByText('Show more')).toBeNull();
    });

    test('renders a string value as plain text with a prettified label', () => {
        renderArgs({channel_id: 'abc123'});
        expect(screen.getByText('Channel Id')).not.toBeNull();
        expect(screen.getByText('abc123')).not.toBeNull();
    });

    test('renders number and boolean values as pills', () => {
        renderArgs({limit: 42, include_thread: true});
        expect(screen.getByText('Limit')).not.toBeNull();
        expect(screen.getByText('42')).not.toBeNull();
        expect(screen.getByText('Include Thread')).not.toBeNull();
        expect(screen.getByText('true')).not.toBeNull();
    });

    test('renders a primitive array as a row of pills', () => {
        renderArgs({usernames: ['alice', 'bob', 'carol']});
        expect(screen.getByText('Usernames')).not.toBeNull();
        expect(screen.getByText('alice')).not.toBeNull();
        expect(screen.getByText('bob')).not.toBeNull();
        expect(screen.getByText('carol')).not.toBeNull();
    });

    test('renders a nested object as an inline JSON block', () => {
        const {container} = renderArgs({filter: {status: 'open', tags: ['a', 'b']}});
        expect(screen.getByText('Filter')).not.toBeNull();
        const pre = container.querySelector('pre');
        expect(pre).not.toBeNull();
        expect(pre?.textContent).toContain('"status": "open"');
        expect(pre?.textContent).toContain('"tags"');
    });

    test('renders null values as a null pill', () => {
        renderArgs({root_id: null});
        expect(screen.getByText('Root Id')).not.toBeNull();
        expect(screen.getByText('null')).not.toBeNull();
    });

    test('preserves argument key insertion order', () => {
        const {container} = renderArgs({beta: 'b', alpha: 'a', gamma: 'g'});
        const text = container.textContent ?? '';
        expect(text.indexOf('Beta')).toBeLessThan(text.indexOf('Alpha'));
        expect(text.indexOf('Alpha')).toBeLessThan(text.indexOf('Gamma'));
    });

    describe('view raw toggle', () => {
        test('swaps the field list for the exact JSON payload and back', () => {
            const {container} = renderArgs({channel_id: 'c1', message: 'hello'});

            // Field list initially: prettified label shown, no raw <pre>.
            expect(screen.getByText('Channel Id')).not.toBeNull();
            expect(container.querySelector('pre')).toBeNull();

            fireEvent.click(screen.getByText('View raw'));

            const pre = container.querySelector('pre');
            expect(pre).not.toBeNull();
            expect(pre?.textContent).toBe(JSON.stringify({channel_id: 'c1', message: 'hello'}, null, 2));

            // The prettified label is gone while raw is shown.
            expect(screen.queryByText('Channel Id')).toBeNull();

            fireEvent.click(screen.getByText('Hide raw'));
            expect(screen.getByText('Channel Id')).not.toBeNull();
            expect(container.querySelector('pre')).toBeNull();
        });

        test('is present whenever there are renderable fields', () => {
            renderArgs({q: 'short'});
            expect(screen.getByText('View raw')).not.toBeNull();
        });
    });

    describe('show more toggle', () => {
        const longText = 'x'.repeat(400);

        test('does not render when all values are short', () => {
            renderArgs({q: 'short', limit: 10});
            expect(screen.queryByText('Show more')).toBeNull();
        });

        test('renders and toggles when a value is long', () => {
            renderArgs({query: longText});
            const showMore = screen.getByText('Show more');
            expect(showMore).not.toBeNull();

            fireEvent.click(showMore);
            expect(screen.getByText('Show less')).not.toBeNull();
            expect(screen.queryByText('Show more')).toBeNull();

            fireEvent.click(screen.getByText('Show less'));
            expect(screen.getByText('Show more')).not.toBeNull();
        });

        test('a multi-line string counts as long', () => {
            renderArgs({message: 'line one\nline two\nline three\nline four'});
            expect(screen.getByText('Show more')).not.toBeNull();
        });
    });

    test('falls back to the raw block for a top-level array (non-object args)', () => {
        const {container} = renderArgs(['a', 'b'] as unknown as JSONValue);
        const pre = container.querySelector('pre');
        expect(pre).not.toBeNull();
        expect(pre?.textContent).toBe(JSON.stringify(['a', 'b'], null, 2));
        expect(screen.queryByText('View raw')).toBeNull();
    });

    test('view raw payload matches the arguments exactly (approval inspectability)', () => {
        const args = {channel_id: 'c1', nested: {a: 1, b: [2, 3]}, flag: false};
        const {container} = renderArgs(args);
        fireEvent.click(screen.getByText('View raw'));
        const pre = within(container).getByText((_content, node) => node?.tagName === 'PRE');
        expect(pre.textContent).toBe(JSON.stringify(args, null, 2));
    });
});

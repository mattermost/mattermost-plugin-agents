// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, waitFor} from '@testing-library/react';

import {IntItem} from './number_items';

describe('IntItem', () => {
    test('keeps an allowEmpty input clear while it is focused', async () => {
        const onChange = jest.fn();
        const {container} = render(
            <IntItem
                label='Max tool turns'
                value={30}
                onChange={onChange}
                allowEmpty={true}
                defaultValue={30}
            />,
        );
        const input = container.querySelector('input') as HTMLInputElement;

        fireEvent.focus(input);
        fireEvent.change(input, {target: {value: ''}});

        expect(input.value).toBe('');
        expect(onChange).toHaveBeenCalledWith(30);

        fireEvent.blur(input);

        await waitFor(() => expect(input.value).toBe('30'));
    });

    test('can report values above max instead of clamping them immediately', () => {
        const onChange = jest.fn();
        const {container} = render(
            <IntItem
                label='Max tool turns'
                value={30}
                onChange={onChange}
                max={250}
                clampOnChange={false}
            />,
        );
        const input = container.querySelector('input') as HTMLInputElement;

        fireEvent.focus(input);
        fireEvent.change(input, {target: {value: '300'}});

        expect(input.value).toBe('300');
        expect(onChange).toHaveBeenCalledWith(300);
    });
});

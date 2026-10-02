// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render} from '@testing-library/react';

import {chooseOption, getSelect, selectedLabel} from '../../../tests/compass_select';

import {SelectionItem} from './item';

const services = [
    {value: 'svc-a', label: 'Service A'},
    {value: 'svc-b', label: 'Service B'},
];

describe('SelectionItem', () => {
    it.each([
        {
            name: 'an unset value without a "none" option shows the placeholder',
            value: '',
            options: services,
            shown: 'Fallback Service',
        },
        {
            name: 'an unset value with a "none" option shows that option',
            value: '',
            options: [{value: '', label: 'No fallback'}, ...services],
            shown: 'No fallback',
        },
        {
            name: 'a set value shows its option',
            value: 'svc-b',
            options: [{value: '', label: 'No fallback'}, ...services],
            shown: 'Service B',
        },
    ])('$name', ({value, options, shown}) => {
        render(
            <SelectionItem
                label='Fallback Service'
                value={value}
                options={options}
                onChange={jest.fn()}
            />,
        );

        expect(selectedLabel(getSelect('Fallback Service'))).toBe(shown);
    });

    it.each([
        {option: 'Service A', reported: 'svc-a'},
        {option: 'No fallback', reported: ''},
    ])('reports "$reported" when "$option" is chosen', ({option, reported}) => {
        const onChange = jest.fn();
        render(
            <SelectionItem
                label='Fallback Service'
                value='svc-b'
                options={[{value: '', label: 'No fallback'}, ...services]}
                onChange={onChange}
            />,
        );

        chooseOption(getSelect('Fallback Service'), option);

        expect(onChange).toHaveBeenCalledWith(reported);
    });
});

// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import {chooseOption, getSelect, selectedLabel} from '../../../tests/compass_select';

import {ComboboxItem, SelectionItem} from './item';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) =>
            defaultMessage.replace(/\{(\w+)\}/g, (_, key: string) => values?.[key] ?? ''),
    };
    return {
        ...actual,
        useIntl: () => intl,
    };
});

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

describe('ComboboxItem', () => {
    const models = [{id: 'gpt-4o', displayName: 'gpt-4o'}];

    it.each([
        {name: 'clears the model', value: 'gpt-4o', canClear: true},
        {name: 'offers no clear button when not clearable', isClearable: false, value: 'gpt-4o', canClear: false},
        {name: 'offers no clear button without a value', value: '', canClear: false},
    ])('$name', ({isClearable, value, canClear}) => {
        const onChange = jest.fn();
        render(
            <ComboboxItem
                label='Model'
                value={value}
                options={models}
                isClearable={isClearable}
                onChange={onChange}
            />,
        );

        const clear = screen.queryByRole('button', {name: 'Clear Model'});
        expect(Boolean(clear)).toBe(canClear);
        if (clear) {
            fireEvent.click(clear);
            expect(onChange).toHaveBeenCalledWith('');
        }
    });
});

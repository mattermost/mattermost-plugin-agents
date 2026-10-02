// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, within} from '@testing-library/react';

type Name = string | RegExp;

export function getSelect(name: Name): HTMLElement {
    return screen.getByRole('combobox', {name});
}

export function openSelect(select: HTMLElement): HTMLElement[] {
    if (select.getAttribute('aria-expanded') !== 'true') {
        fireEvent.click(select);
    }
    const listbox = screen.getByRole('listbox');
    return within(listbox).getAllByRole('option');
}

export function chooseOption(select: HTMLElement, option: Name) {
    const options = openSelect(select);
    const match = options.find((el) => (typeof option === 'string' ? el.textContent === option : option.test(el.textContent ?? '')));
    if (!match) {
        throw new Error(`No option ${String(option)} among: ${options.map((el) => el.textContent).join(', ')}`);
    }
    fireEvent.pointerUp(match);
}

export function optionLabels(select: HTMLElement): string[] {
    const labels = openSelect(select).map((el) => el.textContent ?? '');
    fireEvent.keyDown(select, {key: 'Escape'});
    return labels;
}

export function selectedLabel(select: HTMLElement): string {
    return select.textContent ?? '';
}

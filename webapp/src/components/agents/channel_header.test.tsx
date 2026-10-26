// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import ChannelHeader from './channel_header';

const InfoAction = () => (
    <button type='button'>{'Info'}</button>
);

describe('ChannelHeader', () => {
    it('renders a named header landmark with the name as an h1', () => {
        render(
            <ChannelHeader
                name='Ada'
                ariaLabel='Chat with Ada'
            />,
        );
        expect(screen.getByRole('banner', {name: 'Chat with Ada'})).toBeTruthy();
        expect(screen.getByRole('heading', {level: 1, name: 'Ada'})).toBeTruthy();
    });

    it.each([
        {description: '', expected: 0},
        {description: 'Planning the launch', expected: 1},
    ])('secondary text %#: renders $expected', ({description, expected}) => {
        render(
            <ChannelHeader
                name='Ada'
                description={description}
            />,
        );
        expect(screen.queryAllByText('Planning the launch')).toHaveLength(expected);
    });

    it.each([
        {withActions: false, expectedButtons: 0, expectedChildren: 1},
        {withActions: true, expectedButtons: 1, expectedChildren: 2},
    ])('actions slot %#: buttons=$expectedButtons, no empty container', ({withActions, expectedButtons, expectedChildren}) => {
        const {container} = render(
            <ChannelHeader
                name='Ada'
                actions={withActions ? <InfoAction/> : null}
            />,
        );
        expect(screen.queryAllByRole('button')).toHaveLength(expectedButtons);
        expect(container.querySelector('header')?.children).toHaveLength(expectedChildren);
    });

    it('renders a custom name slot inside the heading and keeps it keyboard operable', () => {
        const onOpen = jest.fn();
        render(
            <ChannelHeader
                name='Ada'
                nameSlot={(
                    <button
                        type='button'
                        aria-haspopup='menu'
                        onClick={onOpen}
                    >{'Ada'}</button>
                )}
            />,
        );
        const trigger = screen.getByRole('button', {name: 'Ada'});
        expect(screen.getByRole('heading', {level: 1}).contains(trigger)).toBe(true);
        trigger.focus();
        expect(document.activeElement).toBe(trigger);
        fireEvent.click(trigger);
        expect(onOpen).toHaveBeenCalledTimes(1);
    });

    it('default name button is focusable and calls onNameClick', () => {
        const onNameClick = jest.fn();
        render(
            <ChannelHeader
                name='Ada'
                onNameClick={onNameClick}
            />,
        );
        const button = screen.getByRole('button', {name: 'Ada'});
        button.focus();
        expect(document.activeElement).toBe(button);
        fireEvent.click(button);
        expect(onNameClick).toHaveBeenCalledTimes(1);
    });
});

// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {useSelector} from 'react-redux';

import manifest from '@/manifest';

import {CitationComponent} from './citation_component';
import {Annotation} from './types';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
    };
});

jest.mock('react-redux', () => ({
    useSelector: jest.fn(),
}));

const mockUseSelector = useSelector as unknown as jest.Mock;

function setAllowUnsafeLinks(allowUnsafeLinks?: boolean) {
    const pluginState = typeof allowUnsafeLinks === 'boolean' ? {allowUnsafeLinks} : {};
    mockUseSelector.mockImplementation((selector) => selector({
        entities: {
            general: {config: {SiteURL: 'http://localhost:8065'}},
            teams: {currentTeamId: 'team_1'},
        },
        ['plugins-' + manifest.id]: pluginState,
    }));
}

function makeAnnotation(overrides: Partial<Annotation> = {}): Annotation {
    return {
        type: 'url_citation',
        start_index: 0,
        end_index: 5,
        url: 'https://attacker.example/path?a=b',
        index: 1,
        ...overrides,
    };
}

function renderComponent(annotation: Annotation) {
    return render(<CitationComponent annotation={annotation}/>);
}

let windowOpenSpy: jest.SpyInstance;

beforeEach(() => {
    windowOpenSpy = jest.spyOn(window, 'open').mockImplementation(() => null);
});

afterEach(() => {
    windowOpenSpy.mockRestore();
    jest.clearAllMocks();
});

describe('CitationComponent with unsafe links disallowed', () => {
    beforeEach(() => {
        setAllowUnsafeLinks(false);
    });

    test('does not navigate when the citation badge is clicked', () => {
        renderComponent(makeAnnotation());

        fireEvent.click(screen.getByTestId('llm-citation'));

        expect(windowOpenSpy).not.toHaveBeenCalled();
    });

    test('does not navigate when the citation badge is activated by keyboard', () => {
        renderComponent(makeAnnotation());

        const badge = screen.getByTestId('llm-citation');
        fireEvent.keyDown(badge, {key: 'Enter'});
        fireEvent.keyDown(badge, {key: ' '});

        expect(windowOpenSpy).not.toHaveBeenCalled();
    });

    test('is not exposed as an interactive control', () => {
        renderComponent(makeAnnotation());

        const badge = screen.getByTestId('llm-citation');
        expect(badge.getAttribute('role')).toBeNull();
        expect(badge.getAttribute('tabindex')).toBeNull();
    });

    test('does not render an image sourced from the citation domain', () => {
        const {container} = renderComponent(makeAnnotation());

        expect(container.querySelector('img')).toBeNull();
        expect(screen.queryByText('attacker.example')).not.toBeNull();
    });

    test('does not render an image for a citation URL that cannot be parsed', () => {
        const {container} = renderComponent(makeAnnotation({url: 'attacker.example/favicon.ico?x=#'}));

        expect(container.querySelector('img')).toBeNull();
    });

    test('does not navigate for a javascript scheme citation URL', () => {
        renderComponent(makeAnnotation({url: 'javascript:alert(1)'})); // eslint-disable-line no-script-url

        fireEvent.click(screen.getByTestId('llm-citation'));

        expect(windowOpenSpy).not.toHaveBeenCalled();
    });

    test('does not navigate when the plugin state has no stored value', () => {
        setAllowUnsafeLinks();
        renderComponent(makeAnnotation());

        fireEvent.click(screen.getByTestId('llm-citation'));

        expect(windowOpenSpy).not.toHaveBeenCalled();
    });
});

describe('CitationComponent with unsafe links allowed', () => {
    beforeEach(() => {
        setAllowUnsafeLinks(true);
    });

    test('navigates to the citation URL when clicked', () => {
        renderComponent(makeAnnotation());

        fireEvent.click(screen.getByTestId('llm-citation'));

        expect(windowOpenSpy).toHaveBeenCalledWith('https://attacker.example/path?a=b', '_blank', 'noopener,noreferrer');
    });

    test('navigates to the citation URL when activated by keyboard', () => {
        renderComponent(makeAnnotation());

        fireEvent.keyDown(screen.getByTestId('llm-citation'), {key: 'Enter'});

        expect(windowOpenSpy).toHaveBeenCalledTimes(1);
    });

    test('is exposed as an interactive control', () => {
        renderComponent(makeAnnotation());

        const badge = screen.getByTestId('llm-citation');
        expect(badge.getAttribute('role')).toBe('button');
        expect(badge.getAttribute('tabindex')).toBe('0');
    });

    test('renders the favicon for the citation domain', () => {
        const {container} = renderComponent(makeAnnotation());

        expect(container.querySelector('img')?.getAttribute('src')).toBe('https://attacker.example/favicon.ico');
    });

    test('does not navigate when the annotation has no URL', () => {
        const annotation = makeAnnotation();
        delete annotation.url;
        renderComponent(annotation);

        fireEvent.click(screen.getByTestId('llm-citation'));

        expect(windowOpenSpy).not.toHaveBeenCalled();
    });

    test('is not exposed as an interactive control when the annotation has no URL', () => {
        const annotation = makeAnnotation();
        delete annotation.url;
        renderComponent(annotation);

        const badge = screen.getByTestId('llm-citation');
        expect(badge.getAttribute('role')).not.toBe('button');
        expect(badge.getAttribute('tabindex')).toBeNull();
    });
});

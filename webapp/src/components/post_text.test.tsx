// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import PostText from './post_text';

jest.mock('react-redux', () => ({
    useSelector: jest.fn(),
}));

jest.mock('./citations/citation_component', () => ({
    CitationComponent: () => null,
}));

// Mirrors the anchors core's markdown renderer emits: internal links carry
// data-link with the site-relative path, external links open in a new tab.
const renderedHTML = [
    '<a href="http://localhost:8065/_redirect/pl/abc123" data-link="/_redirect/pl/abc123"><strong>internal</strong></a>',
    '<a href="https://example.com/page" target="_blank">external</a>',
].join(' ');

describe('PostText internal links', () => {
    const push = jest.fn();

    beforeEach(() => {
        push.mockClear();
        (window as any).PostUtils = {
            formatText: () => renderedHTML,
            messageHtmlToComponent: (html: string) => <span dangerouslySetInnerHTML={{__html: html}}/>,
        };
        window.WebappUtils = {sendWebSocketMessage: jest.fn(), browserHistory: {push}};
    });

    afterEach(() => {
        delete (window as any).PostUtils;
        delete window.WebappUtils;
    });

    test.each([
        {name: 'plain click on internal link navigates in-app', link: 'internal', init: {}, expectedPush: '/_redirect/pl/abc123'},
        {name: 'ctrl-click on internal link keeps browser behavior', link: 'internal', init: {ctrlKey: true}, expectedPush: null},
        {name: 'meta-click on internal link keeps browser behavior', link: 'internal', init: {metaKey: true}, expectedPush: null},
        {name: 'middle-click on internal link keeps browser behavior', link: 'internal', init: {button: 1}, expectedPush: null},
        {name: 'external link keeps browser behavior', link: 'external', init: {}, expectedPush: null},
    ])('$name', ({link, init, expectedPush}) => {
        render(
            <PostText
                message='ignored'
                channelID='channel'
                postID='post'
            />,
        );

        const notPrevented = fireEvent.click(screen.getByText(link), init);

        if (expectedPush) {
            expect(push).toHaveBeenCalledWith(expectedPush);
            expect(notPrevented).toBe(false);
        } else {
            expect(push).not.toHaveBeenCalled();
            expect(notPrevented).toBe(true);
        }
    });
});

describe('PostText streaming reveal', () => {
    beforeEach(() => {
        (window as any).PostUtils = {
            formatText: (text: string) => text,
            messageHtmlToComponent: (html: string) => <p>{html}</p>,
        };
    });

    afterEach(() => {
        delete (window as any).PostUtils;
    });

    test.each([
        {name: 'without reveal, one pass over the whole message', reveal: false, annotations: undefined, paragraphs: 1}, // eslint-disable-line no-undefined
        {name: 'with reveal, block by block', reveal: true, annotations: undefined, paragraphs: 2}, // eslint-disable-line no-undefined
        {name: 'with reveal but citations, one pass', reveal: true, annotations: [{type: 'url_citation' as const, start_index: 0, end_index: 3, index: 1}], paragraphs: 1},
    ])('$name', ({reveal, annotations, paragraphs}) => {
        render(
            <IntlProvider locale='en'>
                <PostText
                    message={'One\n\nTwo words'}
                    channelID='channel'
                    postID='post'
                    annotations={annotations}
                    reveal={reveal ? {fresh: [{id: 1, start: 5, end: 14}], onFreshEnd: jest.fn()} : undefined} // eslint-disable-line no-undefined
                />
            </IntlProvider>,
        );
        const container = screen.getByTestId('posttext');
        expect(container.querySelectorAll(':scope > p')).toHaveLength(paragraphs);
        expect(container.querySelector('[data-fresh-text]') !== null).toBe(reveal && !annotations);
    });
});

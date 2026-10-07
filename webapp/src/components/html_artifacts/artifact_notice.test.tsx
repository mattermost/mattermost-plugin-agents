// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';

import ArtifactNotice from './artifact_notice';

jest.mock('react-intl', () => ({
    ...jest.requireActual('react-intl'),
    useIntl: () => ({locale: 'en', formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage}),
}));

const FULL_SENTENCE = 'AI-generated content. Never enter passwords or sensitive information into it.';

describe('ArtifactNotice', () => {
    it.each([
        {name: 'wide only (fullscreen)', collapseBelowPx: 0, expectShort: false},
        {name: 'collapsible (inline card)', collapseBelowPx: 400, expectShort: true},
    ])('keeps a readable warning: $name', ({collapseBelowPx, expectShort}) => {
        render(<ArtifactNotice collapseBelowPx={collapseBelowPx}/>);

        const notice = screen.getByTestId('html-artifact-notice');
        expect(notice.getAttribute('aria-label')).toBe(FULL_SENTENCE);
        expect(notice.getAttribute('title')).toBe(FULL_SENTENCE);
        expect(screen.getByTestId('html-artifact-notice-full').textContent).toBe('AI-generated · Don\'t enter passwords');

        // Narrow cards swap to the short text instead of hiding the warning.
        const short = screen.queryByTestId('html-artifact-notice-short');
        if (expectShort) {
            expect(short?.textContent).toBe('Don\'t enter passwords');
        } else {
            expect(short).toBeNull();
        }
    });
});

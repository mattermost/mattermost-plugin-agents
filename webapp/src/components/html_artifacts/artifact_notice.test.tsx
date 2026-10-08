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
        {name: 'always inline (fullscreen)', wrapBelowPx: 0},
        {name: 'wraps to its own row when narrow (inline card)', wrapBelowPx: 640},
    ])('shows the full warning text: $name', ({wrapBelowPx}) => {
        render(<ArtifactNotice wrapBelowPx={wrapBelowPx}/>);

        const notice = screen.getByTestId('html-artifact-notice');
        expect(notice.getAttribute('role')).toBe('note');
        expect(notice.getAttribute('aria-label')).toBe(FULL_SENTENCE);
        expect(notice.getAttribute('title')).toBe(FULL_SENTENCE);
        expect(notice.textContent).toBe('AI-generated · Don\'t enter passwords');
    });
});

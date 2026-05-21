// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {insertAnnotationMarkers} from './citation_processor';
import {Annotation} from './types';

describe('insertAnnotationMarkers', () => {
    it('inserts markers by end index descending', () => {
        const annotations: Annotation[] = [
            {
                type: 'url_citation',
                start_index: 0,
                end_index: 2,
                url: 'https://example.com/early',
                index: 1,
            },
            {
                type: 'url_citation',
                start_index: 0,
                end_index: 4,
                url: 'https://example.com/late',
                index: 2,
            },
        ];

        expect(insertAnnotationMarkers('abcdef', annotations)).toBe('ab!!CITE1!!cd!!CITE2!!ef');
    });

    it('preserves citation order for identical insertion points', () => {
        const annotations: Annotation[] = [
            {
                type: 'url_citation',
                start_index: 6,
                end_index: 6,
                url: 'https://example.com/one',
                index: 1,
            },
            {
                type: 'url_citation',
                start_index: 6,
                end_index: 6,
                url: 'https://example.com/two',
                index: 2,
            },
        ];

        expect(insertAnnotationMarkers('abcdef', annotations)).toBe('abcdef!!CITE1!!!!CITE2!!');
    });
});

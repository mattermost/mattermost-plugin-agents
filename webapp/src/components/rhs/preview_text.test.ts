// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {previewText} from './preview_text';

// The `html` of each case mirrors what the host's markdown renderer emits for `source`.
describe('previewText', () => {
    const formatText = jest.fn<string, [string, {singleline: boolean}]>();

    beforeEach(() => {
        formatText.mockReset();
        (window as any).PostUtils = {formatText};
    });

    afterEach(() => {
        delete (window as any).PostUtils;
    });

    test.each([
        {
            name: 'plain text',
            source: 'Key selling points',
            html: '<p class="markdown__paragraph-inline">Key selling points</p>',
            expected: 'Key selling points',
        },
        {
            name: 'emphasis and a link keep their text, not the url',
            source: '**Bold** idea, see [the docs](https://example.com/docs)',
            html: '<p><strong>Bold</strong> idea, see <a class="theme markdown__link" href="https://example.com/docs">the docs</a></p>',
            expected: 'Bold idea, see the docs',
        },
        {
            name: 'paragraphs and headings do not run together',
            source: '## Status\n\nAll good\n\nNext',
            html: '<h2 class="markdown__heading">Status</h2><p>All good</p><p>Next</p>',
            expected: 'Status All good Next',
        },
        {
            name: 'list items do not run together',
            source: '- one\n- two',
            html: '<ul class="markdown__list"><li>one</li><li>two</li></ul>',
            expected: 'one two',
        },
        {
            name: 'table cells do not run together',
            source: '| A | B |\n| - | - |\n| 1 | 2 |',
            html: '<div class="table-responsive"><table><thead><tr><th>A</th><th>B</th></tr></thead><tbody><tr><td>1</td><td>2</td></tr></tbody></table></div>',
            expected: 'A B 1 2',
        },
        {
            name: 'a line break is a space',
            source: 'one  \ntwo',
            html: '<p>one<br>two</p>',
            expected: 'one two',
        },
        {
            name: 'fenced code keeps its text',
            source: 'Run\n\n```ts\nconst n = 1;\n```\nnow',
            html: '<p>Run</p><div data-codeblock-code="const n = 1;" data-codeblock-language="ts" data-codeblock-searchedcontent=""></div><p>now</p>',
            expected: 'Run const n = 1; now',
        },
        {
            name: 'inline code keeps its text',
            source: 'Use `Array<string>` here',
            html: '<p>Use <span class="codespan__pre-wrap"><code>Array&lt;string&gt;</code></span> here</p>',
            expected: 'Use Array<string> here',
        },
        {
            name: 'entities decode to characters',
            source: 'a & b',
            html: '<p>a &amp; b</p>',
            expected: 'a & b',
        },
    ])('$name', ({source, html, expected}) => {
        formatText.mockReturnValue(html);

        expect(previewText(source)).toBe(expected);
        expect(formatText).toHaveBeenCalledWith(source, {singleline: true});
    });

    test('a blank message is empty without calling the formatter', () => {
        expect(previewText('  \n ')).toBe('');
        expect(formatText).not.toHaveBeenCalled();
    });
});

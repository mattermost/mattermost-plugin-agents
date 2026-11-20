// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Block ends that must not run the next block's text into this one once tags are dropped.
const BLOCK_END = /<\/(?:p|li|h[1-6]|t[dh]|tr|blockquote|div)>|<br\s*\/?>/gi;

// Plain text for a chat-history list preview, from the host's markdown formatter.
// The stored message is left as written.
export function previewText(source: string): string {
    if (source.trim() === '') {
        return '';
    }

    const html = window.PostUtils.formatText(source, {singleline: true});
    const doc = new DOMParser().parseFromString(html.replace(BLOCK_END, '$& '), 'text/html');

    // The host renders fenced code as an empty element that carries the code in an attribute.
    doc.querySelectorAll('[data-codeblock-code]').forEach((el) => {
        el.append(` ${el.getAttribute('data-codeblock-code') ?? ''} `);
    });

    return (doc.body.textContent ?? '').replace(/\s+/g, ' ').trim();
}

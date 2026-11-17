// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Plain text for a chat-history list preview. The stored message is left as written;
// only this snippet drops markdown syntax so the list stays readable.
export function stripMarkdownPreview(source: string): string {
    let text = source.replace(/\r\n/g, '\n');

    text = stripCodeFences(text);
    text = stripLinksAndImages(text);
    text = text.replace(/<\/?[A-Za-z][^>\n]*>/g, ' ');
    text = stripBlockSyntax(text);

    // Emphasis and code spans can wrap a line break. Flatten first so a pair still matches.
    text = text.replace(/\s+/g, ' ').trim();
    text = stripEmphasis(text);
    text = text.replace(/`+/g, '');
    text = text.replace(/\\([\\`*_{}[\]()#+\-.!|>~])/g, '$1');

    return text.replace(/\s+/g, ' ').trim();
}

// Fenced blocks keep their code and lose the fence plus any language tag.
function stripCodeFences(text: string): string {
    let next = text;
    for (let i = 0; i < 10; i++) {
        const replaced = next.replace(/^[ \t]*(`{3,}|~{3,})[^\n]*\n([\s\S]*?)^[ \t]*\1[ \t]*$/gm, (_match, _fence, body: string) => body);
        if (replaced === next) {
            break;
        }
        next = replaced;
    }

    // A fence that opens and closes on one line.
    next = next.replace(/(`{3,}|~{3,})([^\n]*?)\1/g, (_match, _fence, body: string) => body);

    // An opening fence that never closes, then any marker still left.
    next = next.replace(/^[ \t]*(`{3,}|~{3,})[^\n]*$/gm, '');
    next = next.replace(/`{3,}|~{3,}/g, '');
    return next;
}

function stripLinksAndImages(text: string): string {
    let next = text;

    // The optional bang covers images. The label (or alt text) is what stays.
    next = next.replace(/!?\[([^\]]*)\]\((?:[^()]|\([^()]*\))*\)/g, '$1');
    next = next.replace(/!?\[([^\]]*)\]\[[^\]]*\]/g, '$1');

    // Reference definitions are not part of the sentence.
    next = next.replace(/^[ \t]{0,3}\[[^\]]+\]:[ \t]*\S.*$/gm, '');

    // Autolinks: the URL is the text.
    next = next.replace(/<((?:https?|mailto):[^>\s]+)>/gi, '$1');

    return next;
}

function stripBlockSyntax(text: string): string {
    let next = text;
    next = next.replace(/^(?:[ \t]*>)+[ \t]?/gm, '');
    next = next.replace(/^[ \t]{0,3}#{1,6}[ \t]+(.*?)[ \t]*#*[ \t]*$/gm, '$1');
    next = next.replace(/^[ \t]*(?:=+|([*_-])(?:[ \t]*\1){2,})[ \t]*$/gm, '');
    next = next.replace(/^[ \t]*(?:[-*+]|\d{1,9}[.)])[ \t]+(?:\[[ xX]\][ \t]+)?/gm, '');
    next = next.split('\n').map(stripTableLine).join('\n');
    return next;
}

// A table row has a leading or trailing pipe, or more than one. A single pipe in a sentence stays.
function stripTableLine(line: string): string {
    const pipes = line.match(/\|/g)?.length ?? 0;
    const trimmed = line.trim();
    const row = pipes >= 2 || trimmed.startsWith('|') || trimmed.endsWith('|');
    if (!row) {
        return line;
    }
    if ((/^[:|\-\s]+$/).test(trimmed)) {
        return '';
    }
    return line.replace(/^\s*\|/, '').replace(/\|\s*$/, '').replace(/\|/g, ' ');
}

function stripEmphasis(text: string): string {
    let next = text;
    for (let pass = 0; pass < 4; pass++) {
        const before = next;

        // Asterisks may sit against a word (`hello**there**`). A closing mark followed by a
        // word or another asterisk is multiplication or an identifier, not emphasis.
        next = next.replace(/(\*+)([^\s*](?:[^*\n]*[^\s*])?)\1(?=[^\w*]|$)/g, '$2');

        // Underscore emphasis is only the flanked kind, so snake_case and obj.__init__ stay.
        next = next.replace(/(^|[\s(["'])(_{1,3})([^\s_](?:[^_\n]*[^\s_])?)\2(?=[\s)\].,!?;:'"]|$)/g, '$1$3');
        next = next.replace(/~~([^\s~](?:[^~\n]*[^\s~])?)~~/g, '$1');
        if (next === before) {
            break;
        }
    }
    return next;
}

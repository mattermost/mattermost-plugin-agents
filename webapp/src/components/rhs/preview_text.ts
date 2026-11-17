// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

const SLOT = '\uE000';

// Tags a preview should drop. Anything else that looks like <...> (a generic, an email
// autolink, prose up to a later >) stays.
const HTML_TAGS = new Set([
    'a', 'b', 'blockquote', 'br', 'code', 'del', 'div', 'em', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
    'hr', 'i', 'img', 'li', 'ol', 'p', 'pre', 's', 'span', 'strong', 'sub', 'sup', 'table', 'td',
    'th', 'tr', 'u', 'ul',
]);

// Plain text for a chat-history list preview. The stored message is left as written;
// only this snippet drops markdown syntax so the list stays readable.
export function stripMarkdownPreview(source: string): string {
    const slots: string[] = [];
    let text = source.replace(/\r\n/g, '\n');

    // Code is literal, including <string> and backticks. Escapes are held out so emphasis
    // does not eat \*literal\* before the backslash is removed.
    text = protectCode(text, slots);
    text = protectEscapes(text, slots);
    text = stripLinksAndImages(text);
    text = stripHtmlTags(text);
    text = stripBlockSyntax(text);

    // Emphasis can wrap a line break. Flatten first so a pair still matches.
    text = text.replace(/\s+/g, ' ').trim();
    text = stripEmphasis(text);
    text = text.replace(/`+/g, '');
    text = restoreSlots(text, slots);

    return text.replace(/\s+/g, ' ').trim();
}

function slotToken(id: number): string {
    return `${SLOT}${id}${SLOT}`;
}

function hold(slots: string[], value: string): string {
    const id = slots.length;
    slots.push(value);
    return slotToken(id);
}

function restoreSlots(text: string, slots: string[]): string {
    return text.replace(new RegExp(`${SLOT}(\\d+)${SLOT}`, 'g'), (_match, id: string) => slots[Number(id)] ?? '');
}

// Fenced and inline code keep their text. The fence, language tag, and backticks do not.
function protectCode(text: string, slots: string[]): string {
    let next = text;
    for (let i = 0; i < 10; i++) {
        const replaced = next.replace(/^[ \t]*(`{3,}|~{3,})[^\n]*\n([\s\S]*?)^[ \t]*\1[ \t]*$/gm, (_match, _fence, body: string) => hold(slots, body));
        if (replaced === next) {
            break;
        }
        next = replaced;
    }
    next = next.replace(/(`{3,}|~{3,})([^\n]*?)\1/g, (_match, _fence, body: string) => hold(slots, body));
    next = next.replace(/``([^`\n]+)``/g, (_match, body: string) => hold(slots, body));
    next = next.replace(/`([^`\n]+)`/g, (_match, body: string) => hold(slots, body));

    // An opening fence that never closes, then any marker still left.
    next = next.replace(/^[ \t]*(`{3,}|~{3,})[^\n]*$/gm, '');
    next = next.replace(/`{3,}|~{3,}/g, '');
    return next;
}

function protectEscapes(text: string, slots: string[]): string {
    return text.replace(/\\([\\`*_{}[\]()#+\-.!|>~])/g, (_match, ch: string) => hold(slots, ch));
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

function stripHtmlTags(text: string): string {
    return text.replace(/<\/?([A-Za-z][A-Za-z0-9-]*)([^>\n]*)>/g, (match, name: string, rest: string) => (isHtmlTag(name, rest) ? ' ' : match));
}

// rest is whatever sat between the tag name and the closing >.
function isHtmlTag(name: string, rest: string): boolean {
    if (!HTML_TAGS.has(name.toLowerCase())) {
        return false;
    }
    const body = rest.trim().replace(/\/\s*$/, '').trim();
    if (body === '') {
        return true;
    }

    // <a@example.com> is an autolink. A quoted mailto attribute is a real tag.
    if (body.includes('@') && !quotedValueHasAt(body)) {
        return false;
    }
    return (/^(?:[^\s"'=<>`/]+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s"'=<>`]+)\s*)+$/).test(body);
}

function quotedValueHasAt(body: string): boolean {
    const eq = body.indexOf('=');
    if (eq === -1) {
        return false;
    }
    let i = eq + 1;
    while (body[i] === ' ' || body[i] === '\t') {
        i += 1;
    }
    const quote = body[i];
    if (quote !== '"' && quote !== '\'') {
        return false;
    }
    const end = body.indexOf(quote, i + 1);
    return end !== -1 && body.slice(i + 1, end).includes('@');
}

function stripBlockSyntax(text: string): string {
    let next = text;
    next = next.replace(/^(?:[ \t]*>)+[ \t]?/gm, '');
    next = stripHeadings(next);
    next = next.replace(/^[ \t]*(?:=+|([*_-])(?:[ \t]*\1){2,})[ \t]*$/gm, '');
    next = next.replace(/^[ \t]*(?:[-*+]|\d{1,9}[.)])[ \t]+(?:\[[ xX]\][ \t]+)?/gm, '');
    next = stripTables(next);
    return next;
}

// A closing hash run needs a space before it, so a title that ends in `#` keeps it.
function stripHeadings(text: string): string {
    let next = text.replace(/^[ \t]{0,3}#{1,6}[ \t]+(.+?)[ \t]+#+[ \t]*$/gm, '$1');
    next = next.replace(/^[ \t]{0,3}#{1,6}[ \t]+/gm, '');
    return next;
}

// Pipes flatten only for a header row followed by a delimiter row. `a || b` is not a table.
function stripTables(text: string): string {
    const lines = text.split('\n');
    const drop = new Set<number>();
    const isDelimiter = (line: string) => {
        const trimmed = line.trim();
        return trimmed.includes('|') && trimmed.includes('-') && (/^[:|\-\s]+$/).test(trimmed);
    };
    const flatten = (line: string) => line.replace(/^\s*\|/, '').replace(/\|\s*$/, '').replace(/\|/g, ' ');

    for (let i = 0; i < lines.length; i++) {
        if (drop.has(i) || !isDelimiter(lines[i])) {
            continue;
        }
        let header = i - 1;
        while (header >= 0 && lines[header].trim() === '') {
            header -= 1;
        }
        if (header < 0 || drop.has(header) || !lines[header].includes('|')) {
            continue;
        }
        lines[header] = flatten(lines[header]);
        drop.add(i);
        for (let row = i + 1; row < lines.length; row++) {
            if (lines[row].trim() === '') {
                break;
            }
            if (isDelimiter(lines[row])) {
                drop.add(row);
                continue;
            }
            if (!lines[row].includes('|')) {
                break;
            }
            lines[row] = flatten(lines[row]);
        }
    }
    return lines.filter((_, index) => !drop.has(index)).join('\n');
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

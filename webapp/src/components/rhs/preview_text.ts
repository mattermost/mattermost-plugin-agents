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
    let next = protectFencedBlocks(text, slots);
    next = next.replace(/(`{3,}|~{3,})([^\n]*?)\1/g, (_match, _fence, body: string) => hold(slots, body));
    next = protectInlineCode(next, slots);
    next = next.replace(/`{3,}|~{3,}/g, '');
    return next;
}

// A closing fence is the same character and at least as long as the opening one.
// An opening fence with no close keeps the rest of the text as code.
function protectFencedBlocks(text: string, slots: string[]): string {
    const lines = text.split('\n');
    const out: string[] = [];
    for (let i = 0; i < lines.length; i++) {
        const open = (/^[ \t]*(`{3,}|~{3,})(.*)$/).exec(lines[i]);
        if (!open || hasRun(open[2], open[1])) {
            out.push(lines[i]);
            continue;
        }
        const fence = open[1];
        let closeAt = -1;
        for (let j = i + 1; j < lines.length; j++) {
            const close = (/^[ \t]*(`{3,}|~{3,})[ \t]*$/).exec(lines[j]);
            if (close && close[1][0] === fence[0] && close[1].length >= fence.length) {
                closeAt = j;
                break;
            }
        }
        const end = closeAt === -1 ? lines.length : closeAt;
        const body = lines.slice(i + 1, end).join('\n');
        if (body !== '') {
            out.push(hold(slots, body));
        }
        if (closeAt === -1) {
            break;
        }
        i = closeAt;
    }
    return out.join('\n');
}

// True when rest holds a run of fence's character at least as long as fence.
function hasRun(rest: string, fence: string): boolean {
    const mark = fence[0];
    let run = 0;
    for (let i = 0; i < rest.length; i++) {
        run = rest[i] === mark ? run + 1 : 0;
        if (run >= fence.length) {
            return true;
        }
    }
    return false;
}

// A backtick run preceded by an odd number of backslashes is literal punctuation.
function protectInlineCode(text: string, slots: string[]): string {
    let out = '';
    for (let i = 0; i < text.length; i++) {
        if (text[i] !== '`' || escaped(text, i)) {
            out += text[i];
            continue;
        }
        let len = 0;
        while (text[i + len] === '`') {
            len += 1;
        }
        const close = closingRun(text, i + len, len);
        if (close === -1) {
            out += text.slice(i, i + len);
            i += (len - 1);
            continue;
        }
        const body = text.slice(i + len, close);
        out += body === '' ? text.slice(i, close + len) : hold(slots, body);
        i = close + (len - 1);
    }
    return out;
}

function escaped(text: string, index: number): boolean {
    let slashes = 0;
    for (let i = index - 1; i >= 0 && text[i] === '\\'; i--) {
        slashes += 1;
    }
    return slashes % 2 === 1;
}

// The start of a same-length backtick run on this line, or -1.
function closingRun(text: string, from: number, len: number): number {
    for (let j = from; j < text.length; j++) {
        if (text[j] === '\n') {
            return -1;
        }
        if (text[j] !== '`') {
            continue;
        }
        let run = 0;
        while (text[j + run] === '`') {
            run += 1;
        }
        if (run === len) {
            return j;
        }
        j += (run - 1);
    }
    return -1;
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

// Pipes flatten only for a header row directly above a delimiter row. `a || b` is not a table.
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
        const header = i - 1;
        if (header < 0 || lines[header].trim() === '' || drop.has(header) || !lines[header].includes('|')) {
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

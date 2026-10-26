// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type MessageSegment =
    | {type: 'markdown'; text: string}
    | {type: 'ui'; source: string; closed: boolean};

const FENCE_OPEN = /^ {0,3}(`{3,}|~{3,})\s*([^\s`]*)\s*$/;
const UI_LANGUAGES = new Set(['openui-lang', 'openui']);

const isFenceClose = (line: string, fence: string) => {
    const trimmed = line.trim();
    return trimmed.length >= fence.length && trimmed[0] === fence[0] && (/^(`+|~+)$/).test(trimmed);
};

/**
 * Splits a markdown message into prose and OpenUI Lang fenced blocks. Fences of
 * other languages are left intact, including any OpenUI fence nested inside
 * them. An unterminated UI fence (mid-stream) runs to the end of the message.
 */
export function splitGenerativeUI(message: string): MessageSegment[] {
    const segments: MessageSegment[] = [];
    const lines = message.split('\n');

    let markdown: string[] = [];
    let ui: string[] | null = null;
    let fence = '';
    let insideOtherFence = false;

    const flushMarkdown = () => {
        const text = markdown.join('\n');
        if (text.trim() !== '') {
            segments.push({type: 'markdown', text});
        }
        markdown = [];
    };

    for (const line of lines) {
        if (ui) {
            if (isFenceClose(line, fence)) {
                segments.push({type: 'ui', source: ui.join('\n'), closed: true});
                ui = null;
            } else {
                ui.push(line);
            }
            continue;
        }

        if (insideOtherFence) {
            markdown.push(line);
            if (isFenceClose(line, fence)) {
                insideOtherFence = false;
            }
            continue;
        }

        const open = FENCE_OPEN.exec(line);
        if (open) {
            fence = open[1];
            if (UI_LANGUAGES.has(open[2].toLowerCase())) {
                flushMarkdown();
                ui = [];
                continue;
            }
            insideOtherFence = true;
        }
        markdown.push(line);
    }

    flushMarkdown();
    if (ui) {
        segments.push({type: 'ui', source: ui.join('\n'), closed: false});
    }

    return segments;
}

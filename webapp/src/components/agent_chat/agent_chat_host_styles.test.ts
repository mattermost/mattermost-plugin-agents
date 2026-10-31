// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import fs from 'fs';
import path from 'path';

import {HOST_STYLE_HOOKS} from './agent_chat_host_styles';

// Drift tripwire for the host markup the agent chat styles rely on (the
// selectors in agent_chat_host_styles.ts). Each hook is a literal the host
// source must still contain. It runs against a mattermost webapp checkout:
// $MM_WEBAPP_PATH (the `webapp` directory, strict: it must exist), else the
// sibling checkouts ../mattermost/webapp and ../mattermost-wsw/webapp of the
// plugin repo, as scripts/check_editor_contract.mjs looks them up. Without
// one the host layer is skipped.
const repoRoot = path.resolve(__dirname, '..', '..', '..', '..');
const marker = path.join('channels', 'src', 'components', 'advanced_text_editor', 'advanced_text_editor.tsx');

const findHost = (): string | null => {
    const explicit = process.env.MM_WEBAPP_PATH; // eslint-disable-line no-process-env
    if (explicit) {
        return path.resolve(explicit);
    }
    const candidates = [path.join(repoRoot, '..', 'mattermost', 'webapp'), path.join(repoRoot, '..', 'mattermost-wsw', 'webapp')];
    return candidates.find((dir) => fs.existsSync(path.join(dir, marker))) ?? null;
};

const host = findHost();

describe('host style hooks', () => {
    test('every hook names a literal and a host source file', () => {
        expect(HOST_STYLE_HOOKS.length).toBeGreaterThan(0);
        HOST_STYLE_HOOKS.forEach(({token, file}) => {
            expect(token.trim()).not.toBe('');
            expect(file).toMatch(/^channels\/src\/.+\.(tsx?|scss)$/);
        });
    });

    (host ? describe : describe.skip)(`against the host source (${host ?? 'no checkout found'})`, () => {
        test('the checkout is a mattermost webapp', () => {
            expect(fs.existsSync(path.join(host!, marker))).toBe(true);
        });

        test.each(HOST_STYLE_HOOKS.map((hook) => ({...hook})))('$file still contains $token', ({token, file}) => {
            const source = path.join(host!, file);
            expect(fs.existsSync(source)).toBe(true);
            expect(fs.readFileSync(source, 'utf8')).toContain(token);
        });
    });
});

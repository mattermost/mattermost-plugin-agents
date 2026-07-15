#!/usr/bin/env node
// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Dev-only drift tripwire for the window.Components editor contract: type-
// checks scripts/editor_contract/probe.ts, which asserts assignability
// between the mattermost webapp's exported editor prop types and this
// plugin's mirrors (src/types/access_control_editors.ts).
//
// The check needs a mattermost webapp checkout with node_modules installed.
// Lookup order:
//   1. $MM_WEBAPP_PATH (path to the mattermost repo's webapp/ directory)
//   2. ../mattermost-wsw/webapp   (sibling checkout of the plugin repo)
//   3. ../mattermost/webapp       (sibling checkout of the plugin repo)
// When no usable checkout is found the check is skipped with exit code 0, so
// wiring it into CI or `make check-style` is safe.

/* eslint-disable no-console, no-process-env, no-process-exit */

import {spawnSync} from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webappDir = path.resolve(scriptDir, '..');
const repoRoot = path.resolve(webappDir, '..');
const probeDir = path.join(scriptDir, 'editor_contract');

// The host file whose exported types anchor the contract.
const marker = path.join(
    'channels', 'src', 'components', 'admin_console', 'access_control',
    'editors', 'table_editor', 'table_editor.tsx',
);

function findHostWebapp() {
    const candidates = [];
    if (process.env.MM_WEBAPP_PATH) {
        candidates.push(path.resolve(process.env.MM_WEBAPP_PATH));
    }
    candidates.push(path.resolve(repoRoot, '..', 'mattermost-wsw', 'webapp'));
    candidates.push(path.resolve(repoRoot, '..', 'mattermost', 'webapp'));

    for (const candidate of candidates) {
        if (!fs.existsSync(path.join(candidate, marker))) {
            continue;
        }
        if (!fs.existsSync(path.join(candidate, 'node_modules'))) {
            console.log(`check-editor-contract: found ${candidate} but its node_modules are not installed; skipping.`);
            return null;
        }
        return candidate;
    }
    return null;
}

const host = findHostWebapp();
if (!host) {
    console.log('check-editor-contract: no mattermost webapp checkout found (set MM_WEBAPP_PATH to point at one); skipping.');
    process.exit(0);
}

console.log(`check-editor-contract: probing against ${host}`);

// The probe compiles under the HOST's compiler options (baseUrl resolves
// against the host checkout via `extends`), with the probe files and the
// host's ambient declarations in the program. `paths` must be re-declared in
// full (a shallow override replaces the host's), with absolute targets:
// the probe file lives in the plugin tree, so bare host packages like
// @mattermost/types would otherwise resolve against the plugin's own copy.
const tsconfig = {
    extends: path.join(host, 'channels', 'tsconfig.json'),
    compilerOptions: {
        composite: false,
        incremental: false,
        noEmit: true,
        skipLibCheck: true,
        types: [],
        paths: {
            'mattermost-redux/*': [path.join(host, 'channels', 'src', 'packages', 'mattermost-redux', 'src', '*')],
            '@mui/styled-engine': [path.join(host, 'channels', 'node_modules', '@mui', 'styled-engine-sc')],
            '@mattermost/types/*': [path.join(host, 'platform', 'types', 'src', '*')],
        },
    },
    include: [
        path.join(probeDir, '**', '*.ts').split(path.sep).join('/'),
        path.join(host, 'channels', 'src', '**', '*.d.ts').split(path.sep).join('/'),
    ],
};

const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'editor-contract-'));
const tsconfigPath = path.join(tmpDir, 'tsconfig.json');
fs.writeFileSync(tsconfigPath, JSON.stringify(tsconfig, null, 4));

const tscBin = path.join(webappDir, 'node_modules', '.bin', process.platform === 'win32' ? 'tsc.cmd' : 'tsc');
const result = spawnSync(tscBin, ['-p', tsconfigPath, '--pretty', 'false'], {encoding: 'utf8'});

fs.rmSync(tmpDir, {recursive: true, force: true});

if (result.error) {
    console.error(`check-editor-contract: failed to run tsc: ${result.error.message}`);
    process.exit(1);
}

// The synthetic program pulls the transitive closure of the host editor
// files without exactly replicating the host's own build environment, which
// yields incidental diagnostics inside host files (e.g. globals declared by
// build tooling we don't mirror). The contract lives in the probe: only
// diagnostics in files under scripts/editor_contract/ are enforced.
const output = `${result.stdout ?? ''}${result.stderr ?? ''}`;
const probePrefixes = [
    probeDir,
    path.relative(process.cwd(), probeDir),
];
const probeDiagnostics = output.split('\n').filter((line) => {
    if (!(/\(\d+,\d+\): error TS\d+/).test(line)) {
        return false;
    }
    return probePrefixes.some((prefix) => line.startsWith(prefix) || line.startsWith(prefix.split(path.sep).join('/')));
});

if (probeDiagnostics.length > 0) {
    console.error(probeDiagnostics.join('\n'));
    console.error('check-editor-contract: FAILED — the plugin\'s editor prop mirrors have drifted from the mattermost webapp\'s exported types.');
    console.error('Reconcile webapp/src/types/access_control_editors.ts with the host files named in its header comment.');
    process.exit(1);
}

console.log('check-editor-contract: OK — plugin editor prop mirrors match the host contract.');

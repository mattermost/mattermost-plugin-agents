#!/usr/bin/env node
// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Drift tripwire for the window.Components editor contract, in two layers:
// 1. ALWAYS (CI): type-checks probe_snapshot.ts, asserting assignability
//    between the plugin mirrors (src/types/access_control_editors.ts) and the
//    committed host-type snapshot; no mattermost checkout needed.
// 2. WHEN A HOST CHECKOUT IS PRESENT (dev): additionally type-checks probe.ts
//    against the live host source and verifies the committed snapshot is
//    still what the host source generates.
//
// Pass --update-snapshot (npm run update-editor-contract-snapshot) to
// regenerate the snapshot. Host checkout lookup: $MM_WEBAPP_PATH, then the
// sibling checkouts ../mattermost-wsw/webapp and ../mattermost/webapp.

/* eslint-disable no-console, no-process-env, no-process-exit */

import {spawnSync} from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

import {generateSnapshot, parseCompilerOptions} from './editor_contract/generate_snapshot.mjs';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webappDir = path.resolve(scriptDir, '..');
const repoRoot = path.resolve(webappDir, '..');
const probeDir = path.join(scriptDir, 'editor_contract');
const snapshotPath = path.join(webappDir, 'src', 'types', 'host_editor_contract.snapshot.d.ts');
const updateSnapshot = process.argv.includes('--update-snapshot');

const tscBin = path.join(webappDir, 'node_modules', '.bin', process.platform === 'win32' ? 'tsc.cmd' : 'tsc');

// runProbe type-checks one probe file and returns the tsc exit status, full
// output, and the diagnostics attributed to files under
// scripts/editor_contract/. The snapshot probe requires a zero exit status;
// the live-host probe only acts on probe-file diagnostics, since incidental
// diagnostics inside host files are expected outside the host's own build.
function runProbe(tsconfig, label) {
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'editor-contract-'));
    const tsconfigPath = path.join(tmpDir, 'tsconfig.json');
    fs.writeFileSync(tsconfigPath, JSON.stringify(tsconfig, null, 4));

    const result = spawnSync(tscBin, ['-p', tsconfigPath, '--pretty', 'false'], {encoding: 'utf8'});
    fs.rmSync(tmpDir, {recursive: true, force: true});

    if (result.error) {
        console.error(`check-editor-contract: failed to run tsc for the ${label} probe: ${result.error.message}`);
        process.exit(1);
    }

    const output = `${result.stdout ?? ''}${result.stderr ?? ''}`;
    const probePrefixes = [probeDir, path.relative(process.cwd(), probeDir)];
    const probeDiagnostics = output.split('\n').filter((line) => {
        if (!(/\(\d+,\d+\): error TS\d+/).test(line)) {
            return false;
        }
        return probePrefixes.some((prefix) => line.startsWith(prefix) || line.startsWith(prefix.split(path.sep).join('/')));
    });
    return {status: result.status, output, probeDiagnostics};
}

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
            console.log(`check-editor-contract: found ${candidate} but its node_modules are not installed; skipping the live-host checks.`);
            return null;
        }
        return candidate;
    }
    return null;
}

// hostTsconfig builds the synthetic tsconfig compiling the probe under the
// HOST's compiler options plus the host's ambient declarations. `paths` must
// be re-declared in full with absolute targets: the probe lives in the plugin
// tree, so bare host packages would otherwise resolve against the plugin's copy.
function hostTsconfig(host) {
    return {
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
            path.join(probeDir, 'probe.ts').split(path.sep).join('/'),
            path.join(host, 'channels', 'src', '**', '*.d.ts').split(path.sep).join('/'),
        ],
    };
}

// --- Layer 1: snapshot probe (always on; the CI gate) ---

if (!fs.existsSync(snapshotPath) && !updateSnapshot) {
    console.error(`check-editor-contract: missing committed snapshot ${path.relative(webappDir, snapshotPath)}.`);
    console.error('Regenerate it against a mattermost webapp checkout: npm run update-editor-contract-snapshot');
    process.exit(1);
}

// In update mode the committed snapshot is about to be regenerated, so a
// stale snapshot must not block the update; the live-host probe below still
// validates the mirrors before the new snapshot is written.
if (fs.existsSync(snapshotPath) && !updateSnapshot) {
    // Self-contained program: only the probe file is listed and tsc pulls its
    // imports transitively. Deliberately NOT src/types/**/* — that would drag
    // in the webapp component tree, including the generated (gitignored)
    // src/manifest.ts absent on CI. Any diagnostic is therefore a real failure.
    const snapshot = runProbe({
        extends: path.join(webappDir, 'tsconfig.json'),
        compilerOptions: {noEmit: true, skipLibCheck: true},
        include: [
            path.join(probeDir, 'probe_snapshot.ts').split(path.sep).join('/'),
        ],
    }, 'snapshot');
    if (snapshot.status !== 0) {
        console.error(snapshot.output.trimEnd());
        console.error('check-editor-contract: FAILED — the plugin\'s editor prop mirrors have drifted from the pinned host contract snapshot.');
        console.error('Reconcile webapp/src/types/access_control_editors.ts with src/types/host_editor_contract.snapshot.d.ts');
        console.error('(or, if the host contract legitimately moved, regenerate the snapshot: npm run update-editor-contract-snapshot).');
        process.exit(1);
    }
    console.log('check-editor-contract: snapshot probe OK — mirrors match the pinned host contract.');
}

// --- Layer 2: live host probe + snapshot freshness (dev only) ---

const host = findHostWebapp();
if (!host) {
    if (updateSnapshot) {
        console.error('check-editor-contract: --update-snapshot needs a mattermost webapp checkout (set MM_WEBAPP_PATH to point at one).');
        process.exit(1);
    }
    console.log('check-editor-contract: no mattermost webapp checkout found (set MM_WEBAPP_PATH to point at one); skipping the live-host checks.');
    process.exit(0);
}

console.log(`check-editor-contract: probing against ${host}`);
const tsconfig = hostTsconfig(host);

const live = runProbe(tsconfig, 'live host');
if (live.probeDiagnostics.length > 0) {
    console.error(live.probeDiagnostics.join('\n'));
    console.error('check-editor-contract: FAILED — the plugin\'s editor prop mirrors have drifted from the mattermost webapp\'s exported types.');
    console.error('Reconcile webapp/src/types/access_control_editors.ts with the host files named in its header comment.');
    process.exit(1);
}
console.log('check-editor-contract: live probe OK — mirrors match the host checkout.');

// Snapshot freshness: the committed snapshot must match what the live host
// source generates, otherwise CI is enforcing a stale contract.
const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'editor-contract-gen-'));
const genTsconfigPath = path.join(tmpDir, 'tsconfig.json');
fs.writeFileSync(genTsconfigPath, JSON.stringify(tsconfig, null, 4));
let generated;
try {
    generated = generateSnapshot(host, parseCompilerOptions(genTsconfigPath));
} finally {
    fs.rmSync(tmpDir, {recursive: true, force: true});
}

if (updateSnapshot) {
    fs.writeFileSync(snapshotPath, generated);
    console.log(`check-editor-contract: wrote ${path.relative(process.cwd(), snapshotPath)} — review and commit it.`);
    process.exit(0);
}

const committed = fs.readFileSync(snapshotPath, 'utf8');
if (committed !== generated) {
    console.error('check-editor-contract: FAILED — the committed host contract snapshot no longer matches the host checkout.');
    console.error('The host editor types moved; regenerate and commit the snapshot: npm run update-editor-contract-snapshot');
    process.exit(1);
}
console.log('check-editor-contract: snapshot is fresh — matches the host checkout.');

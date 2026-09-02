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
// regenerate the snapshot.
//
// Host checkout lookup:
// - $MM_WEBAPP_PATH, when set, is STRICT: it must contain the marker file and
//   have node_modules installed, otherwise the script exits 1. Sibling
//   detection is never attempted when it is set (CI relies on this being
//   fatal). A relative MM_WEBAPP_PATH resolves against the current working
//   directory (webapp/ when run via npm), so an absolute path is recommended.
// - Otherwise the sibling checkouts ../mattermost-wsw/webapp and
//   ../mattermost/webapp are tried best-effort: a sibling without
//   node_modules is skipped with a WARNING, and if no usable sibling exists
//   the live-host layer is skipped with exit 0.

/* eslint-disable no-console, no-process-env, no-process-exit */

import {spawnSync} from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

import {generateSnapshot, parseCompilerOptions, stripProvenance} from './editor_contract/generate_snapshot.mjs';

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

function hasMarker(candidate) {
    return fs.existsSync(path.join(candidate, marker));
}

function hasNodeModules(candidate) {
    return fs.existsSync(path.join(candidate, 'node_modules'));
}

// findHostWebapp returns the host webapp directory to probe, or null when no
// usable host is available. An explicit MM_WEBAPP_PATH is strict: any problem
// with it is fatal and sibling auto-detection is never attempted.
function findHostWebapp() {
    if (process.env.MM_WEBAPP_PATH) {
        const explicit = path.resolve(process.env.MM_WEBAPP_PATH);
        if (!hasMarker(explicit)) {
            console.error(`check-editor-contract: MM_WEBAPP_PATH=${explicit} is not a mattermost webapp checkout.`);
            console.error(`Expected to find ${path.join(explicit, marker)}`);
            process.exit(1);
        }
        if (!hasNodeModules(explicit)) {
            console.error(`check-editor-contract: MM_WEBAPP_PATH=${explicit} is a mattermost webapp checkout but its node_modules are not installed.`);
            console.error(`Install them first: cd ${explicit} && npm ci`);
            process.exit(1);
        }
        return explicit;
    }

    const siblings = [
        path.resolve(repoRoot, '..', 'mattermost-wsw', 'webapp'),
        path.resolve(repoRoot, '..', 'mattermost', 'webapp'),
    ];
    for (const candidate of siblings) {
        if (!hasMarker(candidate)) {
            continue;
        }
        if (!hasNodeModules(candidate)) {
            console.warn(`check-editor-contract: WARNING: found mattermost webapp checkout ${candidate} but its node_modules are not installed; SKIPPING the live-host checks.`);
            console.warn(`To enable them: cd ${candidate} && npm ci (or set MM_WEBAPP_PATH to a checkout with node_modules installed).`);
            continue;
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
        console.error('check-editor-contract: --update-snapshot needs a mattermost webapp checkout with node_modules installed, but none was found.');
        console.error('Provide one by setting MM_WEBAPP_PATH, or by cloning mattermost as a sibling of this repo (../mattermost/webapp or ../mattermost-wsw/webapp) and running npm ci in it.');
        process.exit(1);
    }
    console.log('check-editor-contract: no usable mattermost webapp checkout found (set MM_WEBAPP_PATH, or clone one as ../mattermost/webapp or ../mattermost-wsw/webapp with node_modules installed); skipping the live-host checks.');
    process.exit(0);
}

console.log(`check-editor-contract: probing against ${host}`);
const tsconfig = hostTsconfig(host);

const live = runProbe(tsconfig, 'live host');

// Config-level tsc failures have no (line,col) position, so they never land
// in probeDiagnostics. A non-zero status with no positioned diagnostics means
// the program could not be built at all (bad extends, unresolved paths).
if (live.status !== 0 && !(/\(\d+,\d+\): error TS\d+/).test(live.output)) {
    console.error(live.output.trimEnd());
    console.error('check-editor-contract: FAILED — could not type-check the live-host probe; check the host checkout.');
    process.exit(1);
}
if (live.probeDiagnostics.length > 0) {
    console.error(live.probeDiagnostics.join('\n'));
    console.error('check-editor-contract: FAILED — the plugin\'s editor prop mirrors have drifted from the mattermost webapp\'s exported types.');
    console.error('Reconcile webapp/src/types/access_control_editors.ts with the host files named in its header comment.');
    process.exit(1);
}
console.log('check-editor-contract: live probe OK — mirrors match the host checkout.');

// Snapshot freshness: the committed snapshot must match what the live host
// source generates, otherwise CI is enforcing a stale contract. The
// "Generated from mattermost commit" header line is provenance only and is
// ignored, so a host checkout at a different commit with identical exported
// types is still fresh.
const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'editor-contract-gen-'));
const genTsconfigPath = path.join(tmpDir, 'tsconfig.json');
fs.writeFileSync(genTsconfigPath, JSON.stringify(tsconfig, null, 4));
let generated;
try {
    generated = generateSnapshot(host, parseCompilerOptions(genTsconfigPath));
} finally {
    fs.rmSync(tmpDir, {recursive: true, force: true});
}

const committed = fs.existsSync(snapshotPath) ? fs.readFileSync(snapshotPath, 'utf8').replace(/\r\n/g, '\n') : null;
const typesUnchanged = committed !== null && stripProvenance(committed) === stripProvenance(generated);

if (updateSnapshot) {
    // Only rewrite when the exported types actually changed; a host checkout at
    // a different commit must not churn the provenance header on its own.
    if (typesUnchanged) {
        console.log(`check-editor-contract: ${path.relative(process.cwd(), snapshotPath)} already up to date (types unchanged); leaving existing provenance.`);
        process.exit(0);
    }
    fs.writeFileSync(snapshotPath, generated);
    console.log(`check-editor-contract: wrote ${path.relative(process.cwd(), snapshotPath)} — review and commit it.`);
    process.exit(0);
}

if (!typesUnchanged) {
    console.error('check-editor-contract: FAILED — the committed host contract snapshot no longer matches the host checkout.');
    console.error('The host editor types moved; regenerate and commit the snapshot: npm run update-editor-contract-snapshot');
    process.exit(1);
}
console.log('check-editor-contract: snapshot is fresh — matches the host checkout.');

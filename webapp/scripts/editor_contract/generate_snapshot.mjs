// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Generates src/types/host_editor_contract.snapshot.d.ts from a mattermost
// webapp checkout: resolves the host's exported editor contract types with
// the TypeScript compiler API and prints each as a fully flattened structural
// type, so the snapshot is self-contained (no host imports) and CI can
// type-check the plugin's mirrors against it without a host checkout.
//
// Used by scripts/check_editor_contract.mjs both to (re)generate the
// snapshot (--update-snapshot) and to verify the committed snapshot still
// matches the live host source when a checkout is present.

/* eslint-disable no-console */

import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webappDir = path.resolve(scriptDir, '..', '..');

const require = createRequire(path.join(webappDir, 'package.json'));
const ts = require('typescript');

// The host declarations anchoring the contract; also named in the snapshot
// header as the source of truth.
export const HOST_SOURCES = [
    {
        file: ['channels', 'src', 'components', 'admin_console', 'access_control', 'editors', 'table_editor', 'table_editor.tsx'],
        exports: {TableEditorProps: 'HostTableEditorProps'},
    },
    {
        file: ['channels', 'src', 'components', 'admin_console', 'access_control', 'editors', 'cel_editor', 'editor.tsx'],
        exports: {CELEditorProps: 'HostCELEditorProps', CELEditorActions: 'HostCELEditorActions'},
    },
    {
        file: ['platform', 'types', 'src', 'access_control.ts'],
        exports: {AccessControlTestResult: 'HostAccessControlTestResult', CELExpressionError: 'HostCELExpressionError'},
    },
    {
        file: ['platform', 'types', 'src', 'properties_user.ts'],
        exports: {UserPropertyField: 'HostUserPropertyField'},
    },
];

const MAX_DEPTH = 12;

// React's recursive UI types are impossible to flatten (ReactNode is
// cyclic); the plugin webapp depends on react itself, so the snapshot
// references them through a react type-import instead.
const REACT_PASSTHROUGH = new Set(['ReactNode', 'ReactElement', 'ReactPortal', 'CSSProperties']);

function flattenType(checker, type, depth, seen) {
    if (depth > MAX_DEPTH) {
        return 'unknown';
    }

    const flags = type.flags;
    if (flags & ts.TypeFlags.StringLiteral) {
        return JSON.stringify(type.value);
    }
    if (flags & ts.TypeFlags.NumberLiteral) {
        return String(type.value);
    }
    if (flags & ts.TypeFlags.BooleanLiteral) {
        return type.intrinsicName;
    }
    if (flags & ts.TypeFlags.String) {
        return 'string';
    }
    if (flags & ts.TypeFlags.Number) {
        return 'number';
    }
    if (flags & ts.TypeFlags.Boolean) {
        return 'boolean';
    }
    if (flags & ts.TypeFlags.BigInt) {
        return 'bigint';
    }
    if (flags & ts.TypeFlags.ESSymbolLike) {
        return 'symbol';
    }
    if (flags & ts.TypeFlags.Void) {
        return 'void';
    }
    if (flags & ts.TypeFlags.Undefined) {
        return 'undefined';
    }
    if (flags & ts.TypeFlags.Null) {
        return 'null';
    }
    if (flags & ts.TypeFlags.Never) {
        return 'never';
    }
    if (flags & ts.TypeFlags.Any) {
        return 'any';
    }
    if (flags & ts.TypeFlags.Unknown) {
        return 'unknown';
    }

    const symbolName = type.aliasSymbol?.name ?? type.symbol?.name;
    if (symbolName && REACT_PASSTHROUGH.has(symbolName)) {
        return `React.${symbolName}`;
    }

    if (type.isUnion()) {
        return type.types.map((t) => wrapIfComposite(flattenType(checker, t, depth, seen))).join(' | ');
    }
    if (type.isIntersection()) {
        return type.types.map((t) => wrapIfComposite(flattenType(checker, t, depth, seen))).join(' & ');
    }

    const typeId = type.id ?? type;
    if (seen.has(typeId)) {
        return 'unknown';
    }
    seen.add(typeId);
    try {
        if (checker.isArrayType(type)) {
            const [elem] = checker.getTypeArguments(type);
            return `Array<${flattenType(checker, elem, depth + 1, seen)}>`;
        }
        if (checker.isTupleType(type)) {
            const elems = checker.getTypeArguments(type).map((t) => flattenType(checker, t, depth + 1, seen));
            return `[${elems.join(', ')}]`;
        }
        if (type.symbol?.name === 'Promise' && (type.resolvedTypeArguments?.length || checker.getTypeArguments(type).length)) {
            const [inner] = checker.getTypeArguments(type);
            return `Promise<${flattenType(checker, inner, depth + 1, seen)}>`;
        }

        const callSignatures = type.getCallSignatures();
        const properties = type.getProperties();
        if (callSignatures.length === 1 && properties.length === 0) {
            return signatureToText(checker, callSignatures[0], depth, seen);
        }

        // General object type: flatten every property and index signature.
        const parts = [];
        for (const indexInfo of checker.getIndexInfosOfType(type)) {
            const keyText = flattenType(checker, indexInfo.keyType, depth + 1, seen);
            const valueText = flattenType(checker, indexInfo.type, depth + 1, seen);
            parts.push(`[key: ${keyText}]: ${valueText};`);
        }
        for (const prop of properties) {
            const optional = (prop.flags & ts.SymbolFlags.Optional) !== 0;
            const location = prop.valueDeclaration ?? prop.declarations?.[0];
            const propType = location ?
                checker.getTypeOfSymbolAtLocation(prop, location) :
                checker.getDeclaredTypeOfSymbol(prop);
            const name = (/^[A-Za-z_$][A-Za-z0-9_$]*$/).test(prop.name) ? prop.name : JSON.stringify(prop.name);
            parts.push(`${name}${optional ? '?' : ''}: ${flattenType(checker, propType, depth + 1, seen)};`);
        }
        for (const sig of callSignatures) {
            parts.push(`${signatureToText(checker, sig, depth, seen)};`);
        }
        if (parts.length === 0) {
            return '{}';
        }
        const indent = '    '.repeat(depth + 1);
        const closeIndent = '    '.repeat(depth);
        return `{\n${parts.map((p) => indent + p).join('\n')}\n${closeIndent}}`;
    } finally {
        seen.delete(typeId);
    }
}

function wrapIfComposite(text) {
    if (text.includes('=>') || text.includes(' | ') || text.includes(' & ')) {
        return `(${text})`;
    }
    return text;
}

function signatureToText(checker, signature, depth, seen) {
    const params = signature.parameters.map((param) => {
        const decl = param.valueDeclaration ?? param.declarations?.[0];
        const paramType = decl ?
            checker.getTypeOfSymbolAtLocation(param, decl) :
            checker.getDeclaredTypeOfSymbol(param);
        const optional = decl && ts.isParameter(decl) && (Boolean(decl.questionToken) || Boolean(decl.initializer));
        const rest = decl && ts.isParameter(decl) && Boolean(decl.dotDotDotToken);
        const text = flattenType(checker, paramType, depth + 1, seen);
        return `${rest ? '...' : ''}${param.name}${optional ? '?' : ''}: ${text}`;
    });
    const returnType = flattenType(checker, signature.getReturnType(), depth + 1, seen);
    return `(${params.join(', ')}) => ${returnType}`;
}

// parseCompilerOptions loads the compiler options from a tsconfig file
// (following `extends`), for building the generator program.
export function parseCompilerOptions(tsconfigPath) {
    const parsed = ts.getParsedCommandLineOfConfigFile(tsconfigPath, undefined, {
        ...ts.sys,
        onUnRecoverableConfigFileDiagnostic: (diagnostic) => {
            throw new Error(ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n'));
        },
    });
    return {...parsed.options, noEmit: true, skipLibCheck: true};
}

// generateSnapshot resolves the contract types against the host checkout and
// returns the snapshot file contents. compilerOptions must resolve modules
// against the host (see the synthetic tsconfig in check_editor_contract.mjs).
export function generateSnapshot(host, compilerOptions) {
    const rootNames = HOST_SOURCES.map((source) => path.join(host, ...source.file));
    for (const rootName of rootNames) {
        if (!fs.existsSync(rootName)) {
            throw new Error(`host contract source missing: ${rootName}`);
        }
    }

    const program = ts.createProgram({rootNames, options: compilerOptions});
    const checker = program.getTypeChecker();

    const declarations = [];
    for (const source of HOST_SOURCES) {
        const sourceFile = program.getSourceFile(path.join(host, ...source.file));
        if (!sourceFile) {
            throw new Error(`host contract source not in program: ${source.file.join('/')}`);
        }
        const moduleSymbol = checker.getSymbolAtLocation(sourceFile);
        const exports = checker.getExportsOfModule(moduleSymbol);
        for (const [hostName, snapshotName] of Object.entries(source.exports)) {
            const symbol = exports.find((e) => e.name === hostName);
            if (!symbol) {
                throw new Error(`host no longer exports ${hostName} from ${source.file.join('/')}`);
            }
            const type = checker.getDeclaredTypeOfSymbol(symbol);
            const text = flattenType(checker, type, 0, new Set());
            declarations.push(`export type ${snapshotName} = ${text};`);
        }
    }

    const sourceList = HOST_SOURCES.map((source) => ` *   - webapp/${source.file.join('/')}`).join('\n');
    return `// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/**
 * GENERATED FILE — DO NOT EDIT BY HAND.
 *
 * Pinned snapshot of the mattermost webapp's exported editor contract types,
 * flattened to be self-contained. CI type-checks the plugin's mirrors
 * (src/types/access_control_editors.ts, src/types/access_control.ts) against
 * this snapshot via \`npm run check-editor-contract\`, so contract drift fails
 * the build without needing a host checkout.
 *
 * Source of truth (in the mattermost repo):
${sourceList}
 *
 * Regenerate against a mattermost webapp checkout (and re-verify) with:
 *   MM_WEBAPP_PATH=/path/to/mattermost/webapp npm run update-editor-contract-snapshot
 * (MM_WEBAPP_PATH is optional when the checkout is a sibling of this repo.)
 */

/* eslint-disable */

import type React from 'react';

${declarations.join('\n\n')}
`;
}

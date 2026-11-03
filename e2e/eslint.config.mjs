import tsParser from '@typescript-eslint/parser';

import e2e from './eslint-rules/index.mjs';

export default [
    {ignores: ['playwright-report/', 'test-results/', 'blob-report/', 'logs/']},
    {
        files: ['**/*.ts'],
        languageOptions: {parser: tsParser},
        plugins: {e2e},
        linterOptions: {reportUnusedDisableDirectives: 'error'},
        rules: {
            'e2e/container-exec-exit-code': 'error',
            'e2e/container-start-in-fixture': 'error',
        },
    },
    {
        files: ['tests/**/*.spec.ts'],
        rules: {
            'e2e/scoped-tool-label': 'error',
        },
    },
];

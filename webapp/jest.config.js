// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

module.exports = {
    testEnvironment: 'jsdom',
    transform: {
        '^.+\\.tsx?$': 'ts-jest',

        // react-intl and the @formatjs packages it depends on are ESM-only, so
        // they have to be transpiled before Jest's CommonJS runtime can load them.
        // babel.config.js is bypassed on purpose: its formatjs plugin rejects the
        // non-statically-analysable message calls inside react-intl's own source.
        '^.+\\.m?js$': ['babel-jest', {
            babelrc: false,
            configFile: false,
            presets: [['@babel/preset-env', {targets: {node: 'current'}}]],
        }],
    },
    transformIgnorePatterns: [
        '/node_modules/(?!(?:react-intl|intl-messageformat|@formatjs)/)',
    ],
    moduleNameMapper: {

        // Asset mappings must precede the path aliases below: moduleNameMapper
        // is evaluated in order and '^src/(.*)$' would otherwise match asset
        // imports like 'src/../../assets/bot_icon.png' first.
        '\\.(svg|png|jpg|jpeg|gif|webp)$': '<rootDir>/tests/svg_mock.js',
        '^@/(.*)$': '<rootDir>/src/$1',
        '^src/(.*)$': '<rootDir>/src/$1',
    },
    setupFilesAfterEnv: ['<rootDir>/tests/setup.tsx'],
};

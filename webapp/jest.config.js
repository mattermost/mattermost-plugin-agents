// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

module.exports = {
    testEnvironment: 'jsdom',
    transform: {
        '^.+\\.tsx?$': 'ts-jest',

        // @formatjs ships ESM. Transform it in isolation so the project Babel
        // config (styled-components, formatjs AST) is not applied to it.
        'node_modules/@formatjs/.+\\.js$': ['babel-jest', {
            babelrc: false,
            configFile: false,
            presets: [['@babel/preset-env', {targets: {node: 'current'}}]],
        }],
    },
    transformIgnorePatterns: [
        '/node_modules/(?!@formatjs/)',
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

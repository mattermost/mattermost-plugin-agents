// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

const coreJSVersion = require('core-js/package.json').version;

const config = {

    // Minimum supported browsers, per
    // https://docs.mattermost.com/deployment-guide/software-hardware-requirements.html
    // Strings, not numbers: a numeric target loses trailing zeroes, so
    // a version like 16.10 would be read as 16.1.
    targets: {
        chrome: '146',
        firefox: '140',
        edge: '146',
        safari: '26.2',
    },
    presets: [
        ['@babel/preset-env', {
            modules: false,
            debug: false,
            shippedProposals: true,
        }],
        ['@babel/preset-react', {
            runtime: 'automatic',
        }],

        // Babel 8 otherwise keeps value imports that are only used as types.
        ['@babel/preset-typescript', {
            onlyRemoveTypeImports: false,
        }],
    ],
    plugins: [
        ['babel-plugin-polyfill-corejs3', {
            method: 'usage-global',
            version: coreJSVersion,
        }],
        [
            'babel-plugin-styled-components',
            {
                ssr: false,
                fileName: false,
            },
        ],
        [
            'formatjs',
            {
                idInterpolationPattern: '[sha512:contenthash:base64:8]',
                ast: true,
            },
        ],
    ],
};

// Jest needs module transformation
config.env = {
    test: {
        presets: config.presets,
        plugins: config.plugins,
    },

    // Selected by webpack's babel-loader envName for `npm run debug`.
    development: {
        presets: [
            ['@babel/preset-react', {
                runtime: 'automatic',
                development: true,
            }],
        ],
    },
};
config.env.test.presets[0][1].modules = 'auto';

module.exports = config;

// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Ambient module shims for the probe program: the host editor files import
// stylesheets as side effects, which webpack handles in the host build. The
// probe program only includes the probe + the host's own .d.ts files, so the
// stylesheet modules need declarations here.

declare module '*.scss';
declare module '*.css';

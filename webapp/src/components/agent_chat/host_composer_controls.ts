// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The agent chat leaves these host controls out. Another caller of the
// host composer can pass a different list. Older servers ignore the prop.
export const AGENT_HIDDEN_COMPOSER_CONTROLS = ['burnOnRead', 'priority', 'attributes'] as const;


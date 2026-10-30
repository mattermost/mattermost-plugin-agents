// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {IntlShape} from 'react-intl';

// A chat's display title: its title, or "New chat" while it has none (titles are generated after the
// first reply). Every list and title of chats uses it.
export const chatTitle = (intl: Pick<IntlShape, 'formatMessage'>, title: string): string => (
    title.trim() || intl.formatMessage({defaultMessage: 'New chat'})
);

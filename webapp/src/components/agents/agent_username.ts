// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {IntlShape} from 'react-intl';

const agentUsernamePattern = /^[a-z][a-z0-9.\-_]*$/;

// agentUsernameError returns a validation message for an agent username, or ''
// when the value is acceptable. Shared by the agent editor and the import modal.
export function agentUsernameError(intl: IntlShape, username: string): string {
    if (!username.trim()) {
        return intl.formatMessage({defaultMessage: 'Username is required'});
    }
    if (!agentUsernamePattern.test(username)) {
        return intl.formatMessage({defaultMessage: 'Username must start with a letter and contain only lowercase letters, numbers, periods, hyphens, and underscores'});
    }
    return '';
}

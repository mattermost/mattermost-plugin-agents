// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {IntlShape, useIntl} from 'react-intl';

export type MCPToolPolicyValue = 'auto_run_in_dm' | 'auto_run_everywhere' | 'ask' | '';

export const useToolPolicyOptions = () => {
    const intl = useIntl();
    return [
        {value: 'auto_run_in_dm', label: intl.formatMessage({defaultMessage: 'Auto Run (DM)'})},
        {value: 'auto_run_everywhere', label: intl.formatMessage({defaultMessage: 'Auto Run (Everywhere)'})},
        {value: 'ask', label: intl.formatMessage({defaultMessage: 'Ask Every Time'})},
    ];
};

export function formatToolPolicyLabel(intl: IntlShape, policy: string): string {
    switch (policy) {
    case 'auto_run_in_dm':
    case 'auto_run':
        return intl.formatMessage({defaultMessage: 'Auto Run (DM)'});
    case 'auto_run_everywhere':
        return intl.formatMessage({defaultMessage: 'Auto Run (Everywhere)'});
    case 'ask':
    default:
        return intl.formatMessage({defaultMessage: 'Ask Every Time'});
    }
}

// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

type WithTooltipProps = {
    children: React.ReactNode;
    title?: React.ReactNode;
    disabled?: boolean;
};

/**
 * Host WithTooltip pulls in react-intl defineMessage via @mattermost/shared,
 * which crashes under Jest. Map the module here so transitive DotMenu/Dropdown
 * imports do not need a per-suite mock.
 */
export function WithTooltip({children, title, disabled}: WithTooltipProps) {
    return (
        <span
            data-testid='with-tooltip'
            {...(typeof title === 'string' ? {'data-title': title} : {})}
            data-disabled={disabled ? 'true' : 'false'}
        >
            {children}
        </span>
    );
}

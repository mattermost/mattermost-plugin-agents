// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

export type OwnershipFilter = 'all' | 'yours';

type Props = {
    value: OwnershipFilter;
    onChange: (value: OwnershipFilter) => void;
};

const FilterTabs = ({value, onChange}: Props) => {
    const handleAllClick = useCallback(() => {
        onChange('all');
    }, [onChange]);

    const handleYoursClick = useCallback(() => {
        onChange('yours');
    }, [onChange]);

    return (
        <TabBar>
            <TabButton
                $active={value === 'all'}
                onClick={handleAllClick}
                type='button'
            >
                <FormattedMessage defaultMessage='All'/>
            </TabButton>
            <TabButton
                $active={value === 'yours'}
                onClick={handleYoursClick}
                type='button'
            >
                <FormattedMessage defaultMessage='Yours'/>
            </TabButton>
        </TabBar>
    );
};

const TabBar = styled.div`
    display: flex;
    flex-direction: row;
    gap: 4px;
    flex-shrink: 0;
`;

const TabButton = styled.button<{$active: boolean}>`
    padding: 4px 10px;
    border: none;
    border-radius: 4px;
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    cursor: pointer;
    background: ${(p) => (p.$active ? 'rgba(var(--button-bg-rgb, 28, 88, 217), 0.08)' : 'transparent')};
    color: ${(p) => (p.$active ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.64)')};

    &:hover {
        background: ${(p) => (p.$active ? 'rgba(var(--button-bg-rgb, 28, 88, 217), 0.08)' : 'rgba(var(--center-channel-color-rgb), 0.08)')};
    }
`;

export default FilterTabs;

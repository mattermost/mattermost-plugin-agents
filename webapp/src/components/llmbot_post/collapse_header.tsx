// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import styled from 'styled-components';

/**
 * The clickable one-line header of a collapsible section of a bot post —
 * the "Thinking" reasoning row and the tool activity row. Call sites add
 * their own margins.
 */
export const CollapseHeaderRow = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    cursor: pointer;
    user-select: none;

    &:hover {
        color: rgba(var(--center-channel-color-rgb), 0.8);
    }
`;

/** Chevron for a CollapseHeaderRow: points right when collapsed, down when expanded. */
export const CollapseChevron = styled.div<{$expanded: boolean}>`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
    flex-shrink: 0;
    transition: transform 0.2s ease;
    transform: ${(props) => (props.$expanded ? 'rotate(90deg)' : 'rotate(0)')};

    svg {
        width: 14px;
        height: 14px;
    }
`;

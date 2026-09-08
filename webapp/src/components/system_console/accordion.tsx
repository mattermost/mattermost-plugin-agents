// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {ChevronDownIcon, ChevronRightIcon} from '@mattermost/compass-icons/components';

export type AccordionVariant = 'nested' | 'card';

type AccordionProps = {
    title: React.ReactNode;
    expanded: boolean;
    onToggle: () => void;
    children?: React.ReactNode;
    badge?: React.ReactNode;
    headerExtra?: React.ReactNode;
    contentId?: string;
    variant?: AccordionVariant;

    // Keep children mounted while collapsed (visually hidden + inert).
    keepMounted?: boolean;
    contentCollapsed?: boolean;
};

const Accordion = ({
    title,
    expanded,
    onToggle,
    children,
    badge,
    headerExtra,
    contentId,
    variant = 'nested',
    keepMounted = false,
    contentCollapsed,
}: AccordionProps) => {
    const collapsed = contentCollapsed ?? !expanded;
    const showContent = Boolean(children) && (expanded || keepMounted);

    const handleKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            onToggle();
        }
    };

    return (
        <Shell
            $collapsed={keepMounted && collapsed}
            $variant={variant}
        >
            <HeaderBar>
                <Toggle
                    role='button'
                    tabIndex={0}
                    aria-expanded={expanded}
                    aria-controls={contentId}
                    onClick={onToggle}
                    onKeyDown={handleKeyDown}
                >
                    <HeaderLeft>
                        {expanded ? <ChevronDownIcon size={16}/> : <ChevronRightIcon size={16}/>}
                        <Title $variant={variant}>
                            {title}
                        </Title>
                    </HeaderLeft>
                    {badge}
                </Toggle>
                {headerExtra && (
                    <HeaderExtra>
                        {headerExtra}
                    </HeaderExtra>
                )}
            </HeaderBar>
            {showContent && (
                <Content
                    id={contentId}
                    $collapsed={collapsed}
                    $variant={variant}
                    {...collapsedInert(!collapsed)}
                >
                    {children}
                </Content>
            )}
        </Shell>
    );
};

// Omit inert when expanded: React 18 serializes inert={false} as inert="false",
// which browsers still treat as inert.
function collapsedInert(expanded: boolean): {inert?: ''} {
    return expanded ? {} : {inert: ''};
}

const Shell = styled.div<{$collapsed: boolean; $variant: AccordionVariant}>`
    display: flex;
    flex-direction: column;
    align-items: stretch;
    text-align: left;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    overflow: hidden;
    background-color: ${(props) => (props.$variant === 'card' ? 'var(--center-channel-bg)' : 'transparent')};
    ${({$collapsed}) => $collapsed && `
        position: relative;
        overflow: hidden;
    `}
`;

const HeaderBar = styled.div`
    display: flex;
    align-items: center;
    justify-content: flex-start;
    background-color: rgba(var(--center-channel-color-rgb), 0.02);
    text-align: left;
`;

const Toggle = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex: 1;
    min-width: 0;
    padding: 10px 12px;
    cursor: pointer;
    text-align: left;

    &:hover {
        background-color: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

const HeaderLeft = styled.div`
    display: flex;
    align-items: center;
    justify-content: flex-start;
    gap: 8px;
    min-width: 0;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    text-align: left;
`;

const Title = styled.div<{$variant: AccordionVariant}>`
    font-weight: 600;
    text-align: left;
    ${(props) => (props.$variant === 'card' ? `
        font-size: 16px;
        color: var(--center-channel-color);
    ` : `
        font-size: 13px;
        color: rgba(var(--center-channel-color-rgb), 0.72);
    `)}
`;

const HeaderExtra = styled.div`
    display: flex;
    align-items: center;
    flex-shrink: 0;
    padding-right: 8px;
`;

const Content = styled.div<{$collapsed: boolean; $variant: AccordionVariant; inert?: ''}>`
    display: flex;
    flex-direction: column;
    align-items: stretch;
    text-align: left;
    gap: ${(props) => (props.$variant === 'card' ? '16px' : '12px')};
    padding: ${(props) => (props.$variant === 'card' ? '16px' : '12px')};
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    ${({$collapsed}) => $collapsed && `
        visibility: hidden;
        position: absolute;
        left: 0;
        right: 0;
        overflow: hidden;
        clip-path: inset(50%);
        pointer-events: none;
    `}
`;

export default Accordion;

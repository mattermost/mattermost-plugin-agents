// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';

const TabList = styled.div`
	display: flex;
	flex-shrink: 0;
	gap: var(--spacing-xl);
	border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
`;

const handleTabListKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const keys = ['ArrowLeft', 'ArrowRight', 'Home', 'End'];
    if (!keys.includes(e.key)) {
        return;
    }
    const tabs = Array.from(e.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]:not(:disabled)'));
    const current = tabs.indexOf(e.target as HTMLButtonElement);
    if (current === -1) {
        return;
    }

    let next = current;
    if (e.key === 'ArrowRight') {
        next = (current + 1) % tabs.length;
    } else if (e.key === 'ArrowLeft') {
        next = (current + (tabs.length - 1)) % tabs.length;
    } else if (e.key === 'Home') {
        next = 0;
    } else {
        next = tabs.length - 1;
    }

    e.preventDefault();
    tabs[next].focus();
    tabs[next].click();
};

/** Underline tab list used until compass-ui ships the matching Tabs variant. */
export const UnderlineTabs = (props: React.ComponentProps<typeof TabList>) => (
    <TabList
        {...props}
        onKeyDown={handleTabListKeyDown}
    />
);

export const UnderlineTab = styled.button<{$active: boolean}>`
	padding: var(--spacing-m) 0;
	border: none;
	background: none;
	cursor: pointer;
	font-size: var(--font-size-100);
	font-weight: var(--font-weight-semibold);
	color: ${(props) => (props.$active ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.64)')};
	border-bottom: 2px solid ${(props) => (props.$active ? 'var(--button-bg)' : 'transparent')};
	margin-bottom: -1px;
	transition: color var(--duration-quick) var(--ease-transition, ease), border-color var(--duration-quick) var(--ease-transition, ease);

	&:hover:not(:disabled) {
		color: ${(props) => (props.$active ? 'var(--button-bg)' : 'var(--center-channel-color)')};
	}

	&:disabled {
		cursor: not-allowed;
		opacity: 0.48;
	}
`;

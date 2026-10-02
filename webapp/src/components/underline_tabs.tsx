// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import styled from 'styled-components';

/** Underline tab list used until compass-ui ships the matching Tabs variant. */
export const UnderlineTabs = styled.div`
	display: flex;
	flex-shrink: 0;
	gap: var(--spacing-xl);
	border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
`;

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

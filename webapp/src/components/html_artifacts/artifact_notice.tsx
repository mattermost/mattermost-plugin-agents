// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled, {css} from 'styled-components';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {ShieldAlertOutlineIcon} from '@mattermost/compass-icons/components';

interface Props {

    // Container width below which only the icon is shown (its label stays
    // available as a tooltip and to assistive technology). Omit to always
    // show the text.
    collapseBelowPx?: number;
}

// ArtifactNotice is a permanent warning in the host chrome around an
// artifact. The artifact can draw anything inside its frame, including a fake
// sign-in dialog, and can send what the viewer types to an external site, so
// the host reminds viewers not to enter secrets into it.
const ArtifactNotice = ({collapseBelowPx}: Props) => {
    const intl = useIntl();
    const label = intl.formatMessage({defaultMessage: 'AI-generated content. Never enter passwords or sensitive information into it.'});

    return (
        <Notice
            role='note'
            aria-label={label}
            title={label}
            data-testid='html-artifact-notice'
            $collapseBelowPx={collapseBelowPx}
        >
            <Icon
                size='12'
                glyph={<ShieldAlertOutlineIcon/>}
            />
            <NoticeText aria-hidden='true'>
                {intl.formatMessage({defaultMessage: 'AI-generated · Don\'t enter passwords'})}
            </NoticeText>
        </Notice>
    );
};

export default ArtifactNotice;

const NoticeText = styled.span`
    overflow: hidden;
    text-overflow: ellipsis;
`;

const Notice = styled.span<{$collapseBelowPx?: number}>`
    display: inline-flex;
    flex: 0 1 auto;
    min-width: 12px;
    align-items: center;
    gap: 4px;
    max-width: 100%;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    white-space: nowrap;
    cursor: default;

    ${(props) => (props.$collapseBelowPx ? css`
        @container (max-width: ${props.$collapseBelowPx}px) {
            ${NoticeText} {
                display: none;
            }
        }
    ` : '')}
`;

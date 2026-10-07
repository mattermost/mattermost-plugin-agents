// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled, {css} from 'styled-components';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {ShieldAlertOutlineIcon} from '@mattermost/compass-icons/components';

interface Props {

    // Container width below which a shorter warning replaces the full text
    // (the full sentence stays available as a tooltip and to assistive
    // technology). Omit to always show the full text.
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
            <FullText
                aria-hidden='true'
                data-testid='html-artifact-notice-full'
            >
                {intl.formatMessage({defaultMessage: 'AI-generated · Don\'t enter passwords'})}
            </FullText>
            {Boolean(collapseBelowPx) && (
                <ShortText
                    aria-hidden='true'
                    data-testid='html-artifact-notice-short'
                >
                    {intl.formatMessage({defaultMessage: 'Don\'t enter passwords'})}
                </ShortText>
            )}
        </Notice>
    );
};

export default ArtifactNotice;

const FullText = styled.span``;

const ShortText = styled.span`
    display: none;
`;

// The notice never shrinks: in a tight header the file name gives way first.
const Notice = styled.span<{$collapseBelowPx?: number}>`
    display: inline-flex;
    flex: none;
    align-items: center;
    gap: 4px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    white-space: nowrap;
    cursor: default;

    ${(props) => (props.$collapseBelowPx ? css`
        @container (max-width: ${props.$collapseBelowPx}px) {
            ${FullText} {
                display: none;
            }

            ${ShortText} {
                display: inline;
            }
        }
    ` : '')}
`;

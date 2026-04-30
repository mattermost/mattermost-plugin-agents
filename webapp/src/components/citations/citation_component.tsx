// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {LinkVariantIcon} from '@mattermost/compass-icons/components';

import {CitationBase, CitationWrapper} from './citation_base';
import {Annotation} from './types';

interface CitationComponentProps {
    annotation: Annotation;
}

export const CitationComponent = (props: CitationComponentProps) => {
    const intl = useIntl();

    const handleClick = (e: React.MouseEvent | React.KeyboardEvent) => {
        e.preventDefault();
        e.stopPropagation();
        if (props.annotation.url) {
            window.open(props.annotation.url, '_blank', 'noopener,noreferrer');
        }
    };

    const domain = (() => {
        const url = props.annotation.url;
        if (!url) {
            return '';
        }
        try {
            return new URL(url).hostname;
        } catch {
            return url;
        }
    })();

    const ariaLabel = domain ?
        intl.formatMessage({defaultMessage: 'Citation from {domain}'}, {domain}) :
        intl.formatMessage({defaultMessage: 'Citation from unknown source'});

    return (
        <CitationBase
            icon={<CitationIcon size={12}/>}
            tooltipContent={
                <TooltipContent>
                    {domain ? (
                        <>
                            <FaviconIcon domain={domain}/>
                            <TooltipDomain>{domain}</TooltipDomain>
                        </>
                    ) : (
                        <TooltipDomain>
                            {intl.formatMessage({defaultMessage: 'Unknown source'})}
                        </TooltipDomain>
                    )}
                </TooltipContent>
            }
            onClick={handleClick}
            ariaLabel={ariaLabel}
            testId='llm-citation'
            tooltipTestId='llm-citation-tooltip'
            citationIndex={props.annotation.index}
        />
    );
};

interface FaviconIconProps {
    domain: string;
}

const FaviconIcon = (props: FaviconIconProps) => {
    const [showFallback, setShowFallback] = useState(false);

    const faviconUrl = `https://${props.domain}/favicon.ico`;

    if (showFallback) {
        return <FallbackIcon>{'🌐'}</FallbackIcon>;
    }

    return (
        <FaviconImage
            src={faviconUrl}
            alt={`${props.domain} favicon`}
            onError={() => setShowFallback(true)}
            onLoad={() => setShowFallback(false)}
        />
    );
};

const CitationIcon = styled(LinkVariantIcon)`
    color: rgba(var(--center-channel-color-rgb), 0.75);
    transition: color 0.15s ease;

    ${CitationWrapper}:hover &,
    ${CitationWrapper}:focus & {
        color: rgba(var(--center-channel-color-rgb), 0.85);
    }
`;

const TooltipContent = styled.div`
    background: var(--center-channel-color);
    border-radius: 4px;
    box-shadow: 0px 6px 14px 0px rgba(0, 0, 0, 0.12);
    padding: 4px 8px;
    display: flex;
    align-items: center;
    gap: 4px;
    white-space: nowrap;
`;

const FaviconImage = styled.img`
    width: 12px;
    height: 12px;
    border-radius: 2px;
    flex-shrink: 0;
`;

const FallbackIcon = styled.span`
    font-size: 12px;
    line-height: 1;
    width: 12px;
    height: 12px;
    display: flex;
    align-items: center;
    justify-content: center;
`;

const TooltipDomain = styled.span`
    font-family: 'Open Sans', sans-serif;
    font-weight: 600;
    font-size: 12px;
    line-height: 15px;
    color: var(--center-channel-bg);
`;

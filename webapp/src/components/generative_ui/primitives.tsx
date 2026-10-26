// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useSelector} from 'react-redux';
import styled, {css} from 'styled-components';
import {
    AlertCircleOutlineIcon,
    CheckCircleIcon,
    CircleOutlineIcon,
    ClockOutlineIcon,
    OpenInNewIcon,
} from '@mattermost/compass-icons/components';

import manifest from '@/manifest';

import {usePostMarkdownRenderer} from './post_context';

export const Column = styled.div<{$gap?: 's' | 'm' | 'l'}>`
    display: flex;
    flex-direction: column;
    gap: ${({$gap}) => gapToken($gap)};
    min-width: 0;
`;

export const Row = styled.div<{$gap?: 's' | 'm' | 'l'}>`
    display: flex;
    flex-wrap: wrap;
    gap: ${({$gap}) => gapToken($gap)};

    > * {
        flex: 1 1 220px;
        min-width: 0;
    }
`;

function gapToken(gap?: 's' | 'm' | 'l') {
    switch (gap) {
    case 's':
        return 'var(--spacing-xs)';
    case 'l':
        return 'var(--spacing-xl)';
    case 'm':
    case undefined: // eslint-disable-line no-undefined
        return 'var(--spacing-m)';
    default: {
        const unhandled: never = gap;
        return unhandled;
    }
    }
}

export const CardContainer = styled.section`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m);
    padding: var(--spacing-m) var(--spacing-l);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: var(--radius-s);
    background: var(--center-channel-bg);
    box-shadow: 0 2px 3px rgba(0, 0, 0, 0.08);
    min-width: 0;
`;

const CardHeader = styled.header`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxxs);
`;

const CardTitle = styled.h4`
    margin: 0;
    font-family: Metropolis, sans-serif;
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;
    color: var(--center-channel-color);
`;

const CardSubtitle = styled.span`
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

export const Card = ({title, subtitle, children}: {title?: string; subtitle?: string; children: React.ReactNode}) => (
    <CardContainer>
        {(title || subtitle) && (
            <CardHeader>
                {title && <CardTitle>{title}</CardTitle>}
                {subtitle && <CardSubtitle>{subtitle}</CardSubtitle>}
            </CardHeader>
        )}
        {children}
    </CardContainer>
);

export type HeadingSize = 'large' | 'medium' | 'small';

const headingStyles = {
    large: css`font-size: 20px; line-height: 28px;`,
    medium: css`font-size: 16px; line-height: 24px;`,
    small: css`font-size: 14px; line-height: 20px;`,
};

const HeadingText = styled.h3<{$size: HeadingSize}>`
    margin: 0;
    font-family: Metropolis, sans-serif;
    font-weight: 600;
    color: var(--center-channel-color);
    ${({$size}) => headingStyles[$size]}
`;

export const Heading = ({text, size = 'medium'}: {text: string; size?: HeadingSize}) => (
    <HeadingText $size={size}>{text}</HeadingText>
);

export type TextTone = 'default' | 'muted' | 'small';

const TextBody = styled.div<{$tone: TextTone}>`
    font-size: ${({$tone}) => ($tone === 'small' ? '12px' : '14px')};
    line-height: ${({$tone}) => ($tone === 'small' ? '16px' : '20px')};
    color: ${({$tone}) => ($tone === 'default' ? 'var(--center-channel-color)' : 'rgba(var(--center-channel-color-rgb), 0.72)')};
    overflow-wrap: anywhere;

    p {
        margin: 0;
    }

    p + p {
        margin-top: var(--spacing-xs);
    }
`;

export const Text = ({text, tone = 'default'}: {text: string; tone?: TextTone}) => {
    const renderMarkdown = usePostMarkdownRenderer();
    return <TextBody $tone={tone}>{renderMarkdown ? renderMarkdown(text) : text}</TextBody>;
};

export const InlineGroup = styled.div`
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--spacing-xxs);
`;

const FieldGrid = styled.dl`
    display: grid;
    grid-template-columns: minmax(96px, max-content) 1fr;
    gap: var(--spacing-xs) var(--spacing-l);
    margin: 0;
    font-size: 14px;
    line-height: 20px;
`;

const FieldLabel = styled.dt`
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const FieldValue = styled.dd`
    margin: 0;
    color: var(--center-channel-color);
    overflow-wrap: anywhere;
`;

export const Fields = ({items}: {items: Array<{label: string; value: React.ReactNode}>}) => (
    <FieldGrid>
        {items.map((item, index) => (
            <React.Fragment key={`${item.label}-${index}`}>
                <FieldLabel>{item.label}</FieldLabel>
                <FieldValue>{item.value}</FieldValue>
            </React.Fragment>
        ))}
    </FieldGrid>
);

export type StepStatus = 'done' | 'current' | 'todo' | 'blocked';

const StepList = styled.ol`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
    margin: 0;
    padding: 0;
    list-style: none;
`;

const StepItem = styled.li`
    display: grid;
    grid-template-columns: 16px 1fr;
    gap: var(--spacing-xs);
    align-items: start;
`;

const stepColor: Record<StepStatus, string> = {
    done: 'var(--online-indicator)',
    current: 'var(--button-bg)',
    todo: 'rgba(var(--center-channel-color-rgb), 0.48)',
    blocked: 'var(--error-text)',
};

const StepIconWrapper = styled.span<{$status: StepStatus}>`
    display: flex;
    padding-top: 2px;
    color: ${({$status}) => stepColor[$status]};
`;

const StepTitle = styled.span<{$status: StepStatus}>`
    display: block;
    font-size: 14px;
    font-weight: ${({$status}) => ($status === 'current' ? 600 : 400)};
    line-height: 20px;
    color: var(--center-channel-color);
`;

const StepDescription = styled.span`
    display: block;
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const StepIcon = ({status}: {status: StepStatus}) => {
    switch (status) {
    case 'done':
        return <CheckCircleIcon size={16}/>;
    case 'current':
        return <ClockOutlineIcon size={16}/>;
    case 'todo':
        return <CircleOutlineIcon size={16}/>;
    case 'blocked':
        return <AlertCircleOutlineIcon size={16}/>;
    default: {
        const unhandled: never = status;
        return unhandled;
    }
    }
};

export const Steps = ({items}: {items: Array<{title: string; status: StepStatus; description?: string}>}) => (
    <StepList>
        {items.map((item, index) => (
            <StepItem key={`${item.title}-${index}`}>
                <StepIconWrapper $status={item.status}>
                    <StepIcon status={item.status}/>
                </StepIconWrapper>
                <span>
                    <StepTitle $status={item.status}>{item.title}</StepTitle>
                    {item.description && <StepDescription>{item.description}</StepDescription>}
                </span>
            </StepItem>
        ))}
    </StepList>
);

const ExternalLink = styled.a`
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-xxxs);
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--link-color);

    &:hover {
        text-decoration: underline;
    }
`;

const SAFE_LINK_PROTOCOLS = new Set(['http:', 'https:', 'mailto:']);

export function safeLinkURL(url: string): string | null {
    try {
        const parsed = new URL(url);
        return SAFE_LINK_PROTOCOLS.has(parsed.protocol) ? parsed.href : null;
    } catch {
        return null;
    }
}

const UnlinkedURL = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    overflow-wrap: anywhere;
`;

// Follows the plugin's unsafe-links setting like agent post text: when links are
// not allowed, the destination is shown as plain text so a reader can vet it.
export const Link = ({label, url}: {label: string; url: string}) => {
    const allowUnsafeLinks = useSelector<any, boolean>((state) => state['plugins-' + manifest.id]?.allowUnsafeLinks ?? false);
    const href = safeLinkURL(url);
    if (!href) {
        return <span>{label}</span>;
    }
    if (!allowUnsafeLinks) {
        return <UnlinkedURL>{`${label} (${href})`}</UnlinkedURL>;
    }
    return (
        <ExternalLink
            href={href}
            target='_blank'
            rel='noopener noreferrer'
        >
            {label}
            <OpenInNewIcon size={14}/>
        </ExternalLink>
    );
};

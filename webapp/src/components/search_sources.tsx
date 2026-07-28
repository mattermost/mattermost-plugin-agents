// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {PostPreview} from './post_preview';

// Utility for formatting relevance scores
const formatScore = (score: number): string => {
    // Convert to percentage and round to nearest integer
    return `${Math.round(score * 100)}%`;
};

const SourcesContainer = styled.div`
    margin-top: 16px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
`;

const SourcesHeader = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 20px;
    cursor: pointer;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const SourcesTitle = styled.div`
    font-weight: 600;
    font-size: 14px;
    line-height: 20px;
`;

const SourceCount = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.75);
    background: rgba(var(--center-channel-color-rgb), 0.08);
	border-radius: 8px;
	padding: 0 4px;
    margin-left: 8px;
	font-size: 11px;
	font-weight: 700;
	line-height: 16px;
`;

const CollapseIcon = styled.i<{isOpen: boolean}>`
    font-size: 18px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    margin-left: auto;
    transform: ${(props) => (props.isOpen ? 'rotate(180deg)' : 'rotate(0deg)')};
    transition: transform 0.15s ease-in-out;
`;

const SourcesList = styled.div<{isOpen: boolean}>`
    display: ${(props) => (props.isOpen ? 'flex' : 'none')};
    flex-direction: column;
    margin-top: 8px;
`;

const SourceNumber = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.56);
    margin-right: 8px;
`;

const SourceHeader = styled.div`
    display: flex;
    align-items: center;
    margin-bottom: 4px;
`;

const RelevanceScore = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.65);
    font-size: 12px;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    padding: 2px 6px;
    border-radius: 4px;
    margin-left: 8px;
    font-weight: 500;
`;

const ScoreIcon = styled.i`
    font-size: 10px;
    margin-right: 4px;
`;

const SourceItem = styled.div`
    padding: 8px 20px;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);

    &:last-child {
        border-bottom: none;
        padding-bottom: 16px;
    }
`;

interface Source {
    postId: string;
    channelId: string;
    userId: string;
    content: string;
    score: number;
}

interface SourceItemProps {
    source: Source;
}

const SearchSource = ({source, index}: SourceItemProps & {index: number}) => {
    return (
        <SourceItem>
            <SourceHeader>
                <SourceNumber>{index + 1}{'.'}</SourceNumber>
                <RelevanceScore>
                    <ScoreIcon className='icon icon-check-circle'/>
                    {formatScore(source.score)}
                </RelevanceScore>
            </SourceHeader>
            <PostPreview
                postId={source.postId}
                userId={source.userId}
                channelId={source.channelId}
                content={source.content}
            />
        </SourceItem>
    );
};

interface Props {
    sources: Source[];
}

export const SearchSources = ({sources}: Props) => {
    const [isOpen, setIsOpen] = useState(false);

    if (!sources || sources.length === 0) {
        return null;
    }

    return (
        <SourcesContainer>
            <SourcesHeader onClick={() => setIsOpen(!isOpen)}>
                <SourcesTitle>
                    <FormattedMessage defaultMessage='Sources'/>
                    <SourceCount>{sources.length}</SourceCount>
                </SourcesTitle>
                <CollapseIcon
                    className='icon-chevron-down'
                    isOpen={isOpen}
                />
            </SourcesHeader>
            <SourcesList isOpen={isOpen}>
                {sources.map((source, index) => (
                    <SearchSource
                        key={source.postId}
                        index={index}
                        source={source}
                    />
                ))}
            </SourcesList>
        </SourcesContainer>
    );
};

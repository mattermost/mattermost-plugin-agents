// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {isValidId} from '@/utils/ids';

import {PostPreview} from './post_preview';

// Mirrors maxMaxResults in api/api_search.go: the server never returns more
// than this many search results, so anything beyond it is dropped.
export const MAX_SEARCH_SOURCES = 100;

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

// Transient ($-prefixed) props so styled-components doesn't forward them to the DOM.
const CollapseIcon = styled.i<{$isOpen: boolean}>`
    font-size: 18px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    margin-left: auto;
    transform: ${(props) => (props.$isOpen ? 'rotate(180deg)' : 'rotate(0deg)')};
    transition: transform 0.15s ease-in-out;
`;

const SourcesList = styled.div<{$isOpen: boolean}>`
    display: ${(props) => (props.$isOpen ? 'flex' : 'none')};
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

export interface Source {
    postId: string;
    channelId: string;
    userId: string;
    content: string;
    score: number;
}

// toSource accepts an entry only when it is an object referencing well-formed
// ids; the remaining display fields are coerced to safe defaults.
function toSource(entry: unknown): Source | null {
    if (typeof entry !== 'object' || entry === null) {
        return null;
    }
    const {postId, channelId, userId, content, score} = entry as Record<string, unknown>;
    if (!isValidId(postId) || !isValidId(channelId) || !isValidId(userId)) {
        return null;
    }
    return {
        postId,
        channelId,
        userId,
        content: typeof content === 'string' ? content : '',
        score: typeof score === 'number' && Number.isFinite(score) ? score : 0,
    };
}

// parseSearchSources decodes the search_results post prop. Post props are
// free-form JSON, so the value may be missing, not a string, not valid JSON,
// not an array, or hold entries without well-formed ids. Anything unusable is
// dropped rather than thrown so a bad prop can never break the post render,
// and the result is bounded to the server's maximum result count.
export function parseSearchSources(raw: unknown): Source[] {
    if (typeof raw !== 'string' || raw === '') {
        return [];
    }

    let parsed: unknown;
    try {
        parsed = JSON.parse(raw);
    } catch {
        return [];
    }
    if (!Array.isArray(parsed)) {
        return [];
    }

    const sources: Source[] = [];
    for (const entry of parsed) {
        if (sources.length >= MAX_SEARCH_SOURCES) {
            break;
        }
        const source = toSource(entry);
        if (source) {
            sources.push(source);
        }
    }
    return sources;
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
                    $isOpen={isOpen}
                />
            </SourcesHeader>
            <SourcesList $isOpen={isOpen}>
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

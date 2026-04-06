// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useCallback} from 'react';
import styled from 'styled-components';
import {useSelector, useDispatch} from 'react-redux';

import {getCustomPrompts, getPinnedPromptIds} from '@/selectors';
import {fetchCustomPrompts, fetchPinnedPromptIds} from '@/redux';
import {renderCustomPrompt, createPost} from '@/client';

const PillContainer = styled.div`
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    padding: 0;
    margin-bottom: 16px;
`;

const PillButton = styled.button`
    background: rgba(var(--button-bg-rgb), 0.08);
    color: var(--button-bg);
    border: none;
    border-radius: 16px;
    padding: 6px 16px;
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    white-space: nowrap;

    &:hover {
        background: rgba(var(--button-bg-rgb), 0.16);
    }
`;

interface Props {
    channelId: string;
    selectPost: (postId: string) => void;
    setCurrentTab: (tab: string) => void;
}

const RHSPromptButtons = ({channelId, selectPost, setCurrentTab}: Props) => {
    const dispatch = useDispatch();
    const prompts = useSelector(getCustomPrompts);
    const pinnedIds = useSelector(getPinnedPromptIds);

    useEffect(() => {
        dispatch(fetchCustomPrompts() as any);
        dispatch(fetchPinnedPromptIds() as any);
    }, [dispatch]);

    const handleClick = useCallback(async (promptId: string) => {
        try {
            const result = await renderCustomPrompt(promptId, channelId);
            const post = {
                channel_id: channelId,
                message: result.rendered,
                props: {},
                file_ids: [],
            };
            const created = await createPost(post);
            selectPost(created.id);
            setCurrentTab('thread');
        } catch (e) {
            console.error('Failed to execute custom prompt:', e); // eslint-disable-line no-console
        }
    }, [channelId, selectPost, setCurrentTab]);

    const pinnedPrompts = (prompts || []).filter((p) => pinnedIds.includes(p.id));

    if (pinnedPrompts.length === 0) {
        return null;
    }

    return (
        <PillContainer>
            {pinnedPrompts.map((prompt) => (
                <PillButton
                    key={prompt.id}
                    onClick={() => handleClick(prompt.id)}
                >
                    {prompt.name}
                </PillButton>
            ))}
        </PillContainer>
    );
};

export default RHSPromptButtons;

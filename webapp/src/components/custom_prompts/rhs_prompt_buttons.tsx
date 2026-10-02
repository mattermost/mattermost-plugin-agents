// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useCallback} from 'react';
import styled from 'styled-components';
import {useSelector, useDispatch} from 'react-redux';

import {Button} from '@mattermost/compass-ui/components/button';

import {getCustomPrompts, getPinnedPromptIds} from '@/selectors';
import {fetchCustomPrompts, fetchPinnedPromptIds} from '@/redux';
import {renderCustomPrompt, createPost} from '@/client';

const ButtonContainer = styled.div`
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 24px;
    margin-bottom: 24px;
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
        <ButtonContainer>
            {pinnedPrompts.map((prompt) => (
                <Button
                    key={prompt.id}
                    emphasis='tertiary'
                    size='small'
                    onClick={() => handleClick(prompt.id)}
                >
                    {prompt.name}
                </Button>
            ))}
        </ButtonContainer>
    );
};

export default RHSPromptButtons;

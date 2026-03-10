// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect} from 'react';
import styled from 'styled-components';
import {useIntl, FormattedMessage} from 'react-intl';

import {useDispatch, useSelector} from 'react-redux';

import RHSImage from '../assets/rhs_image';

import {createPost, getBotDirectChannel} from '@/client';

import {AdvancedTextEditor, CreatePost} from '@/mm_webapp';

import {LLMBot} from '@/bots';
import {BotsHandler} from '@/redux';
import manifest from '@/manifest';

import {RHSPaddingContainer, RHSText, RHSTitle} from './common';

const CreatePostContainer = styled.div`
	.custom-textarea {
		padding-top: 13px;
		padding-bottom: 13px;
		passing-left: 16px;
	}
    .AdvancedTextEditor {
        padding: 0px;
    }
`;

const ReverseScroll = styled.div`
	display: flex;
	flex-direction: column;
	flex-grow: 1;
	justify-content: flex-end;
`;

type Props = {
    selectPost: (postId: string) => void
    setCurrentTab: (tab: string) => void
    activeBot: LLMBot | null
}

const RHSNewTab = ({selectPost, setCurrentTab, activeBot}: Props) => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const [draft, updateDraft] = useState<any>(null);
    const [creatingChannel, setCreatingChannel] = useState(false);
    const currentUserId = useSelector((state: any) => state.entities.users.currentUserId);
    const botChannelId = activeBot?.dmChannelID || '';

    const currentBots = useSelector((state: any) =>
        state[`plugins-${manifest.id}`]?.bots || [],
    );

    // State for error handling
    const [channelError, setChannelError] = useState(false);

    // If botChannelId is empty, we need to create a direct channel
    useEffect(() => {
        const createDirectChannel = async () => {
            if (!botChannelId && !creatingChannel && activeBot) {
                setCreatingChannel(true);
                setChannelError(false);
                const botId = activeBot.id;

                try {
                    // This will, as a side effect, create the direct channel for us
                    const newChannelID = await getBotDirectChannel(currentUserId, botId);

                    // Update the bots list in Redux with the new channel ID
                    const updatedBots = currentBots.map((bot: LLMBot) => {
                        if (bot.id === activeBot.id) {
                            return {
                                ...bot,
                                dmChannelID: newChannelID,
                            };
                        }
                        return bot;
                    });
                    dispatch({
                        type: BotsHandler,
                        bots: updatedBots,
                    });
                } catch (error) {
                    setChannelError(true);
                } finally {
                    setCreatingChannel(false);
                }
            }
        };
        createDirectChannel();
    }, [botChannelId, currentUserId, activeBot, creatingChannel, dispatch, currentBots]);

    // Show loading indicator if creating channel or error message if failed
    let editorComponent;
    if (channelError) {
        editorComponent = (
            <div style={{textAlign: 'center', padding: '20px', color: 'var(--error-text)'}}>
                <FormattedMessage defaultMessage='Failed to create chat channel. Please try again later.'/>
            </div>
        );
    } else if (creatingChannel || !botChannelId) {
        editorComponent = (
            <div style={{textAlign: 'center', padding: '20px'}}>
                <FormattedMessage defaultMessage='Setting up chat channel...'/>
            </div>
        );
    } else if (AdvancedTextEditor) {
        editorComponent = (
            <AdvancedTextEditor
                channelId={botChannelId}
                placeholder={intl.formatMessage({defaultMessage: 'Ask Agents anything...'})}
                isThreadView={true}
                location={'RHS_COMMENT'}
                afterSubmit={(result: {created?: {id: string}}) => {
                    if (result.created?.id) {
                        selectPost(result.created?.id);
                        setCurrentTab('thread');
                    }
                }}
            />
        );
    } else {
        editorComponent = (
            <CreatePost
                channelId={botChannelId}
                placeholder={intl.formatMessage({defaultMessage: 'Ask Agents anything...'})}
                rootId={'ai_agents'}
                onSubmit={async (p: any) => {
                    const post = {...p};
                    post.channel_id = botChannelId || '';
                    post.props = {};
                    post.uploadsInProgress = [];
                    post.file_ids = p.fileInfos.map((f: any) => f.id);
                    const created = await createPost(post);
                    selectPost(created.id);
                    setCurrentTab('thread');
                    dispatch({
                        type: 'SET_GLOBAL_ITEM',
                        data: {
                            name: 'comment_draft_ai_agents',
                            value: {message: '', fileInfos: [], uploadsInProgress: []},
                        },
                    });
                }}
                draft={draft}
                onUpdateCommentDraft={(newDraft: any) => {
                    updateDraft(newDraft);
                    const timestamp = new Date().getTime();
                    newDraft.updateAt = timestamp;
                    newDraft.createAt = newDraft.createAt || timestamp;
                    dispatch({
                        type: 'SET_GLOBAL_ITEM',
                        data: {
                            name: 'comment_draft_ai_agents',
                            value: newDraft,
                        },
                    });
                }}
            />
        );
    }

    return (
        <RHSPaddingContainer>
            <ReverseScroll>
                <RHSImage/>
                <RHSTitle><FormattedMessage defaultMessage='Ask Agents anything'/></RHSTitle>
                <RHSText><FormattedMessage defaultMessage='Agents are here to help. Ask a question below to get started.'/></RHSText>
                <CreatePostContainer
                    data-testid='rhs-new-tab-create-post'
                >
                    {editorComponent}
                </CreatePostContainer>
            </ReverseScroll>
        </RHSPaddingContainer>
    );
};

export default React.memo(RHSNewTab);

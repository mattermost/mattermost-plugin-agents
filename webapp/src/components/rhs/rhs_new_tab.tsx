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

import RHSPromptButtons from '../custom_prompts/rhs_prompt_buttons';

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

const EMPTY_BOTS: LLMBot[] = [];
const EMPTY_RHS_DRAFT = {message: '', fileInfos: [], uploadsInProgress: []};

const RHSNewTab = ({selectPost, setCurrentTab, activeBot}: Props) => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const [draft, updateDraft] = useState<any>(null);
    const [creatingChannel, setCreatingChannel] = useState(false);
    const currentUserId = useSelector((state: any) => state.entities.users.currentUserId);
    const botChannelId = activeBot?.dmChannelID || '';

    const currentBots = useSelector((state: any) =>
        state[`plugins-${manifest.id}`]?.bots ?? EMPTY_BOTS,
    );

    // State for error handling
    const [channelError, setChannelError] = useState(false);
    const [submitError, setSubmitError] = useState('');

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
    } else if (CreatePost) {
        editorComponent = (
            <CreatePost
                channelId={botChannelId}
                placeholder={intl.formatMessage({defaultMessage: 'Ask Agents anything...'})}
                rootId={'ai_agents'}
                onSubmit={async (p: any) => {
                    try {
                        const post = {...p};
                        post.channel_id = botChannelId || '';
                        post.props = {};
                        post.uploadsInProgress = [];
                        post.file_ids = p.fileInfos.map((f: any) => f.id);
                        const created = await createPost(post);
                        setSubmitError('');
                        updateDraft(EMPTY_RHS_DRAFT);
                        selectPost(created.id);
                        setCurrentTab('thread');
                        dispatch({
                            type: 'SET_GLOBAL_ITEM',
                            data: {
                                name: 'comment_draft_ai_agents',
                                value: EMPTY_RHS_DRAFT,
                            },
                        });
                    } catch (e) {
                        console.error('Failed to create Agents RHS post:', e); // eslint-disable-line no-console
                        setSubmitError(intl.formatMessage({defaultMessage: 'Failed to send message. Please try again.'}));
                    }
                }}
                draft={draft}
                onUpdateCommentDraft={(newDraft: any) => {
                    setSubmitError('');
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
    } else if (AdvancedTextEditor) {
        // Prefer the plugin-scoped CreatePost wrapper when available. The generic
        // AdvancedTextEditor registers file drop handlers as a center-post composer
        // when no real rootId exists, which causes uploads dropped into the center
        // channel to also attach to the Agents RHS draft.
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
        editorComponent = null;
    }

    return (
        <RHSPaddingContainer>
            <ReverseScroll>
                <RHSImage/>
                <RHSTitle><FormattedMessage defaultMessage='Ask Agents anything'/></RHSTitle>
                <RHSText><FormattedMessage defaultMessage='Agents can help you with almost anything. Choose from the prompts below or write your own.'/></RHSText>
                {botChannelId && (
                    <RHSPromptButtons
                        channelId={botChannelId}
                        selectPost={selectPost}
                        setCurrentTab={setCurrentTab}
                    />
                )}
                {submitError && (
                    <div style={{textAlign: 'center', padding: '0 0 12px', color: 'var(--error-text)'}}>
                        {submitError}
                    </div>
                )}
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

// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {ChangeEvent, useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

//@ts-ignore it exists
import aiIcon from 'src/../../assets/bot_icon.png';

import {getBotProfilePictureUrl} from '@/client';

import {TertiaryButton} from '../assets/buttons';

import {ItemLabel} from './item';

type AvatarItemProps = {
    botusername: string;
    changedAvatar: (image: File) => void;
}

const AvatarItem = (props: AvatarItemProps) => {
    const [icon, setIcon] = useState<string>(aiIcon);

    // Tracks whether the user has uploaded a local image in this session. While true, we
    // skip the username-driven fetch so that typing into the username field does not wipe
    // the unsaved upload preview.
    const hasLocalUpload = useRef(false);
    const hiddenInput = useRef<HTMLInputElement>(null);

    // Refetch the avatar whenever botusername changes so this widget can be reused across
    // different bots/agents (e.g. when navigating between agents in the edit page) without
    // showing the previous bot's avatar. Reset to the placeholder before the new fetch
    // resolves to avoid a brief flash of the previous bot's image, and ignore late
    // responses from a stale fetch via a "cancelled" flag.
    useEffect(() => {
        let cancelled = false;
        if (hasLocalUpload.current) {
            return () => {
                cancelled = true;
            };
        }
        setIcon(aiIcon);
        if (!props.botusername) {
            return () => {
                cancelled = true;
            };
        }
        (async () => {
            // Fetches can reject (e.g. 404 while the user is still typing a draft username
            // before the bot exists, or transient auth/network errors). Swallow the rejection
            // and keep the placeholder icon already set above so we never bubble an unhandled
            // promise rejection or wipe state we don't own.
            try {
                const userIcon = await getBotProfilePictureUrl(props.botusername);
                if (!cancelled && userIcon) {
                    setIcon(userIcon);
                }
            } catch {
                // Placeholder is already in place; nothing more to do.
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [props.botusername]);

    const onUploadChange = async (e: ChangeEvent<HTMLInputElement>) => {
        if (e.target.files && e.target.files[0]) {
            const file = e.target.files[0];

            hasLocalUpload.current = true;
            const reader = new FileReader();
            reader.onload = () => {
                setIcon(URL.createObjectURL(file));
            };
            reader.readAsArrayBuffer(file);
            e.target.value = '';
            props.changedAvatar(file);
        } else {
            hasLocalUpload.current = false;
            setIcon(aiIcon);
        }
    };

    return (
        <>
            <ItemLabel><FormattedMessage defaultMessage='Bot avatar'/></ItemLabel>
            <AvatarSelectorContainer>
                <Avatar src={icon}/>
                <TertiaryButton
                    onClick={() => {
                        if (hiddenInput.current) {
                            hiddenInput.current.click();
                        }
                    }}
                >
                    <HiddenInput
                        ref={hiddenInput}
                        type='file'
                        accept='.jpeg,.jpg,.png,.gif' // From the MM server requirements
                        onChange={onUploadChange}
                    />
                    <FormattedMessage defaultMessage='Upload Image'/>
                </TertiaryButton>
            </AvatarSelectorContainer>
        </>
    );
};

const HiddenInput = styled.input`
	&&& {
		display: none;
	}
`;

const Avatar = styled.img`
	width: 64px;
	height: 64px;
	border-radius: 50%;
`;

const AvatarSelectorContainer = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	gap: 16px;
`;

export default AvatarItem;

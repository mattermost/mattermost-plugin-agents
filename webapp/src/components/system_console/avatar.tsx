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
    avatarOwnerKey?: string;
    changedAvatar: (image: File) => void;
}

const AvatarItem = (props: AvatarItemProps) => {
    const [icon, setIcon] = useState<string>(aiIcon);
    const hasLocalUpload = useRef(false);
    const localPreviewURL = useRef<string | null>(null);
    const avatarOwnerKey = useRef(props.avatarOwnerKey);
    const hiddenInput = useRef<HTMLInputElement>(null);

    useEffect(() => {
        if (avatarOwnerKey.current === props.avatarOwnerKey) {
            return;
        }

        avatarOwnerKey.current = props.avatarOwnerKey;
        hasLocalUpload.current = false;

        if (localPreviewURL.current) {
            URL.revokeObjectURL(localPreviewURL.current);
            localPreviewURL.current = null;
        }

        setIcon(aiIcon);
    }, [props.avatarOwnerKey]);

    useEffect(() => {
        return () => {
            if (localPreviewURL.current) {
                URL.revokeObjectURL(localPreviewURL.current);
            }
        };
    }, []);

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
            try {
                const userIcon = await getBotProfilePictureUrl(props.botusername);
                if (!cancelled && userIcon) {
                    setIcon(userIcon);
                }
            } catch {
                // Keep the placeholder for unknown or temporarily unreachable users.
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [props.botusername, props.avatarOwnerKey]);

    const onUploadChange = (e: ChangeEvent<HTMLInputElement>) => {
        if (e.target.files && e.target.files[0]) {
            const file = e.target.files[0];

            hasLocalUpload.current = true;
            if (localPreviewURL.current) {
                URL.revokeObjectURL(localPreviewURL.current);
            }

            localPreviewURL.current = URL.createObjectURL(file);
            setIcon(localPreviewURL.current);
            e.target.value = '';
            props.changedAvatar(file);
        } else {
            hasLocalUpload.current = false;
            if (localPreviewURL.current) {
                URL.revokeObjectURL(localPreviewURL.current);
                localPreviewURL.current = null;
            }
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

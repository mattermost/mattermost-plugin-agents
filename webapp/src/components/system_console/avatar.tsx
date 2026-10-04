// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {ChangeEvent, useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

//@ts-ignore it exists
import aiIcon from 'src/../../assets/bot_icon.png';

import {getBotProfilePictureUrl} from '@/client';

import {TertiaryButton} from '../assets/buttons';

import {FormRow, ItemLabel} from './item';

type AvatarItemProps = {
    botusername: string;
    avatarOwnerKey?: string;
    changedAvatar: (image: File) => void;

    /**
     * The not-yet-saved upload held by the parent, when the parent owns it.
     * The preview then follows this value: it survives remounts (e.g. switching
     * editor tabs) and falls back to the saved avatar when the parent clears it.
     */
    pendingFile?: File | null;
}

type LocalPreview = {
    file: File;
    url: string;
}

const AvatarItem = (props: AvatarItemProps) => {
    const {pendingFile} = props;
    const [serverIcon, setServerIcon] = useState<string>(aiIcon);
    const [preview, setPreview] = useState<LocalPreview | null>(null);
    const previewRef = useRef(preview);
    previewRef.current = preview;
    const avatarOwnerKey = useRef(props.avatarOwnerKey);
    const hiddenInput = useRef<HTMLInputElement>(null);

    // Each preview URL is revoked once it is replaced or the item unmounts.
    useEffect(() => {
        return () => {
            if (preview) {
                URL.revokeObjectURL(preview.url);
            }
        };
    }, [preview]);

    useEffect(() => {
        if (avatarOwnerKey.current === props.avatarOwnerKey) {
            return;
        }
        avatarOwnerKey.current = props.avatarOwnerKey;
        setPreview(null);
    }, [props.avatarOwnerKey]);

    useEffect(() => {
        if (typeof pendingFile === 'undefined') {
            return;
        }
        if (!pendingFile) {
            setPreview(null);
            return;
        }
        if (previewRef.current?.file !== pendingFile) {
            setPreview({file: pendingFile, url: URL.createObjectURL(pendingFile)});
        }
    }, [pendingFile]);

    useEffect(() => {
        let cancelled = false;
        setServerIcon(aiIcon);
        if (!props.botusername) {
            return () => {
                cancelled = true;
            };
        }
        (async () => {
            try {
                const userIcon = await getBotProfilePictureUrl(props.botusername);
                if (!cancelled && userIcon) {
                    setServerIcon(userIcon);
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
            setPreview({file, url: URL.createObjectURL(file)});
            e.target.value = '';
            props.changedAvatar(file);
        } else {
            setPreview(null);
        }
    };

    const icon = preview?.url ?? serverIcon;

    return (
        <FormRow>
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
        </FormRow>
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

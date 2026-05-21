// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {CheckIcon, ContentCopyIcon} from '@mattermost/compass-icons/components';
import {useIntl} from 'react-intl';

import {HelpText, ItemLabel, StyledInput, TextFieldContainer} from './item';

export type CopyableTextItemProps = {
    label: string;
    value: string;
    helptext?: string;
};

export const CopyableTextItem = (props: CopyableTextItemProps) => {
    const intl = useIntl();
    const [copied, setCopied] = useState(false);
    const copyResetTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

    useEffect(() => {
        return () => {
            if (copyResetTimer.current) {
                clearTimeout(copyResetTimer.current);
            }
        };
    }, []);

    const handleCopy = async () => {
        try {
            if (!navigator.clipboard?.writeText) {
                return;
            }

            await navigator.clipboard.writeText(props.value);
            setCopied(true);
            if (copyResetTimer.current) {
                clearTimeout(copyResetTimer.current);
            }
            copyResetTimer.current = setTimeout(() => setCopied(false), 2000);
        } catch (e) {
            // eslint-disable-next-line no-console
            console.error('Failed to copy to clipboard:', e);
        }
    };

    const copyLabel = copied ?
        intl.formatMessage({id: 'p556q3uv', defaultMessage: 'Copied'}) :
        intl.formatMessage({id: 'aCdAsIsV', defaultMessage: 'Copy to clipboard'});

    return (
        <>
            <ItemLabel>{props.label}</ItemLabel>
            <TextFieldContainer>
                <CopyableInputRow>
                    <StyledInput
                        type='text'
                        value={props.value}
                        readOnly={true}
                        aria-label={props.label}
                        onFocus={(e) => e.currentTarget.select()}
                    />
                    <CopyButton
                        type='button'
                        onClick={handleCopy}
                        aria-label={copyLabel}
                        title={copyLabel}
                    >
                        {copied ? <CheckIcon size={16}/> : <ContentCopyIcon size={16}/>}
                    </CopyButton>
                </CopyableInputRow>
                {props.helptext &&
                <HelpText>{props.helptext}</HelpText>
                }
            </TextFieldContainer>
        </>
    );
};

const CopyableInputRow = styled.div`
	display: flex;
	flex-direction: row;
	gap: 8px;
	align-items: stretch;

	& > input {
		flex: 1 1 auto;
		min-width: 0;
	}
`;

const CopyButton = styled.button`
	display: inline-flex;
	align-items: center;
	justify-content: center;
	height: 35px;
	min-width: 35px;
	padding: 0 8px;
	border-radius: 2px;
	border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
	background: var(--center-channel-bg);
	color: rgba(var(--center-channel-color-rgb), 0.72);
	cursor: pointer;

	&:hover {
		background: rgba(var(--center-channel-color-rgb), 0.08);
		color: var(--center-channel-color);
	}

	&:focus {
		border-color: var(--button-bg);
		outline: none;
	}
`;

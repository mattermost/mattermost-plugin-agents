// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {CheckIcon, ContentCopyIcon} from '@mattermost/compass-icons/components';
import {useIntl} from 'react-intl';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {TextInput} from '@mattermost/compass-ui/components/text-input';

import {FormRow, HelpText, ItemLabel, TextFieldContainer} from './item';

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
        <FormRow>
            <ItemLabel>{props.label}</ItemLabel>
            <TextFieldContainer>
                <CopyableInputRow>
                    <TextInput
                        type='text'
                        value={props.value}
                        readOnly={true}
                        aria-label={props.label}
                        onFocus={(e) => e.currentTarget.select()}
                    />
                    <IconButton
                        icon={<Icon glyph={copied ? <CheckIcon/> : <ContentCopyIcon/>}/>}
                        size='small'
                        onClick={handleCopy}
                        aria-label={copyLabel}
                        title={copyLabel}
                    />
                </CopyableInputRow>
                {props.helptext &&
                <HelpText>{props.helptext}</HelpText>
                }
            </TextFieldContainer>
        </FormRow>
    );
};

const CopyableInputRow = styled.div`
	display: flex;
	flex-direction: row;
	gap: 8px;
	align-items: center;

	& > div {
		flex: 1 1 auto;
		min-width: 0;
	}
`;

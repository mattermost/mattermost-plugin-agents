// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {PlusIcon, TrashCanOutlineIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {TextInput} from '@mattermost/compass-ui/components/text-input';

// Renaming a key deletes the old entry; blank rows are legal (the backend ignores them).
export const HeaderMapEditor = ({
    headers,
    onChange,
    namePlaceholder,
    valuePlaceholder,
    disableAdd,
    disableEdit,
}: {
    headers: {[key: string]: string};
    onChange: (headers: {[key: string]: string}) => void;
    namePlaceholder?: string;
    valuePlaceholder?: string;
    disableAdd?: boolean;
    disableEdit?: boolean;
}) => {
    const intl = useIntl();
    const headerNamePlaceholder = namePlaceholder ?? intl.formatMessage({defaultMessage: 'Header name'});
    const headerValuePlaceholder = valuePlaceholder ?? intl.formatMessage({defaultMessage: 'Value'});

    const addHeader = () => {
        onChange({...headers, '': ''});
    };

    // Rebuild in place: appending the renamed entry would reorder rows rendered by index.
    const updateHeader = (oldKey: string, newKey: string, value: string) => {
        const updated: {[key: string]: string} = {};
        for (const [k, v] of Object.entries(headers)) {
            if (k === oldKey) {
                updated[newKey] = value;
            } else {
                updated[k] = v;
            }
        }
        onChange(updated);
    };

    const removeHeader = (key: string) => {
        const updated = {...headers};
        delete updated[key];
        onChange(updated);
    };

    return (
        <>
            <HeadersList>
                {Object.entries(headers).map(([key, value], index) => (
                    <HeaderRow key={index}>
                        <HeaderInput
                            placeholder={headerNamePlaceholder}
                            value={key}
                            disabled={disableEdit}
                            onChange={(e) => updateHeader(key, e.target.value, value)}
                        />
                        <HeaderInput
                            placeholder={headerValuePlaceholder}
                            value={value}
                            disabled={disableEdit}
                            onChange={(e) => updateHeader(key, key, e.target.value)}
                        />
                        <IconButton
                            icon={<Icon glyph={<TrashCanOutlineIcon/>}/>}
                            aria-label={intl.formatMessage({defaultMessage: 'Remove header'})}
                            size='small'
                            destructive={true}
                            onClick={() => removeHeader(key)}
                        />
                    </HeaderRow>
                ))}
            </HeadersList>
            {!disableAdd && (
                <AddHeaderButton
                    emphasis='quaternary'
                    size='small'
                    leadingIcon={<Icon glyph={<PlusIcon/>}/>}
                    onClick={addHeader}
                >
                    <FormattedMessage defaultMessage='Add Header'/>
                </AddHeaderButton>
            )}
        </>
    );
};

const HeadersList = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
`;

const HeaderRow = styled.div`
    display: flex;
    gap: var(--spacing-xs);
    align-items: center;
`;

const HeaderInput = styled(TextInput)`
    flex: 1;
    min-width: 0;
`;

const AddHeaderButton = styled(Button)`
    align-self: flex-start;
`;

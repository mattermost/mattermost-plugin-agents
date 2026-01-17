// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect} from 'react';
import styled from 'styled-components';
import {TrashCanOutlineIcon, ChevronDownIcon, AlertOutlineIcon, ChevronUpIcon, PlusIcon, CloseIcon} from '@mattermost/compass-icons/components';
import {BooleanItem, ItemList, SelectionItem, SelectionItemOption, TextItem, ItemLabel, HelpText, ComboboxItem} from './item';
import {FormattedMessage, useIntl} from 'react-intl';

type CustomHeadersItemProps = {
    customHeaders: {[key: string]: string} | undefined
    onChange: (headers: {[key: string]: string}) => void
}

export const CustomHeadersItem = (props: CustomHeadersItemProps) => {
    const intl = useIntl();
    const headers = Object.entries(props.customHeaders || {});
    
    // Generate stable keys for React to prevent focus loss
    const headersWithStableKeys = headers.map((header, index) => ({
        key: `header-${index}`,
        headerKey: header[0],
        value: header[1]
    }));
    
    const addHeader = () => {
        const newHeaders = {...props.customHeaders};
        // Find a unique placeholder name
        let counter = 1;
        while (newHeaders[`X-Custom-Header-${counter}`]) {
            counter++;
        }
        newHeaders[`X-Custom-Header-${counter}`] = '';
        props.onChange(newHeaders);
    };
    
    const updateHeaderKey = (oldKey: string, newKey: string) => {
        if (oldKey === newKey) return;
        
        const newHeaders = {...props.customHeaders};
        const value = newHeaders[oldKey];
        delete newHeaders[oldKey];
        if (newKey && !newHeaders[newKey]) {
            newHeaders[newKey] = value;
        }
        props.onChange(newHeaders);
    };
    
    const updateHeaderValue = (key: string, value: string) => {
        const newHeaders = {...props.customHeaders};
        newHeaders[key] = value;
        props.onChange(newHeaders);
    };
    
    const removeHeader = (key: string) => {
        const newHeaders = {...props.customHeaders};
        delete newHeaders[key];
        props.onChange(newHeaders);
    };
    
    return (
        <>
            <ItemLabel>
                {intl.formatMessage({defaultMessage: 'Custom Headers'})}
            </ItemLabel>
            <CustomHeadersContainer>
                {headersWithStableKeys.map((item) => (
                    <HeaderRow key={item.key}>
                        <HeaderInput
                            placeholder="Header name (e.g., X-Organization)"
                            value={item.headerKey}
                            onChange={(e) => updateHeaderKey(item.headerKey, e.target.value)}
                        />
                        <HeaderInput
                            placeholder="Header value"
                            value={item.value}
                            onChange={(e) => updateHeaderValue(item.headerKey, e.target.value)}
                        />
                        <RemoveButton
                            type="button"
                            onClick={() => removeHeader(item.headerKey)}
                            title="Remove header"
                        >
                            <CloseIcon size={16} />
                        </RemoveButton>
                    </HeaderRow>
                ))}
                <AddButton
                    type="button"
                    onClick={addHeader}
                >
                    <PlusIcon size={16} />
                    {intl.formatMessage({defaultMessage: 'Add Header'})}
                </AddButton>
                <HelpText>
                    {intl.formatMessage({defaultMessage: 'Custom headers will be sent with every API request to the LLM provider. Use this for authentication, tracking, or routing purposes.'})}
                </HelpText>
            </CustomHeadersContainer>
        </>
    );
};

const CustomHeadersContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`; 

const HeaderRow = styled.div`
    display: flex;
    gap: 8px;
    align-items: center;
`;

const HeaderInput = styled.input`
    flex: 1;
    padding: 8px 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background: rgba(var(--center-channel-bg-rgb), 1);
    color: rgba(var(--center-channel-color-rgb), 1);
    font-size: 14px;
    
    &:focus {
        border-color: var(--button-bg);
        outline: none;
        box-shadow: 0 0 0 2px rgba(var(--button-bg-rgb), 0.2);
    }
    
    &::placeholder {
        color: rgba(var(--center-channel-color-rgb), 0.5);
    }
`;

const AddButton = styled.button`
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 8px 12px;
    border: 1px solid var(--button-bg);
    border-radius: 4px;
    background: transparent;
    color: var(--button-bg);
    font-size: 14px;
    cursor: pointer;
    
    &:hover {
        background: rgba(var(--button-bg-rgb), 0.08);
    }
`;

const RemoveButton = styled.button`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background: transparent;
    color: rgba(var(--center-channel-color-rgb), 0.7);
    cursor: pointer;
    
    &:hover {
        background: rgba(var(--error-text-color-rgb), 0.08);
        border-color: var(--error-text-color);
        color: var(--error-text-color);
    }
`;


export default CustomHeadersItem;
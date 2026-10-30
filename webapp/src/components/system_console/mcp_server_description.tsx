// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';
import {OpenInNewIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';

import {getMCPSuggestedDescription} from '../../client';

import {FormRow, HelpText, ItemLabel, TextFieldContainer, TextItem} from './item';
import {useSettingsReadOnly} from './settings_read_only';

// Mirrors config.MaxMCPServerDescriptionRunes.
export const MAX_MCP_SERVER_DESCRIPTION_LENGTH = 500;

type AutoFill =
    | {status: 'idle'}
    | {status: 'loading'}
    | {status: 'filled'; source: 'description' | 'instructions'}
    | {status: 'empty'}
    | {status: 'needsOAuth'; authURL: string}
    | {status: 'failed'};

type Props = {

    // Set once the server is saved; auto-fill needs the saved configuration.
    serverID?: string;
    description: string;

    // False while the server's connection settings have unsaved changes.
    connectionSaved: boolean;
    onChange: (description: string) => void;

    // Receives an auto-filled description once it arrives, by when the admin
    // may have changed or deleted the server.
    onSuggestion: (description: string) => void;
};

const MCPServerDescription = ({serverID, description, connectionSaved, onChange, onSuggestion}: Props) => {
    const intl = useIntl();
    const readOnly = useSettingsReadOnly();
    const [autoFill, setAutoFill] = useState<AutoFill>({status: 'idle'});
    const canAutoFill = Boolean(serverID) && connectionSaved;

    const fill = async () => {
        if (!serverID) {
            return;
        }
        setAutoFill({status: 'loading'});
        try {
            const suggestion = await getMCPSuggestedDescription(serverID);
            if (suggestion.needsOAuth && suggestion.authURL) {
                setAutoFill({status: 'needsOAuth', authURL: suggestion.authURL});
            } else if (suggestion.source === '' || !suggestion.description) {
                setAutoFill({status: 'empty'});
            } else {
                onSuggestion(suggestion.description);
                setAutoFill({status: 'filled', source: suggestion.source});
            }
        } catch {
            setAutoFill({status: 'failed'});
        }
    };

    let status: React.ReactNode = null;
    if (!canAutoFill) {
        status = intl.formatMessage({defaultMessage: 'Save this server to auto-fill its description from the server.'});
    } else if (autoFill.status === 'filled' && autoFill.source === 'description') {
        status = intl.formatMessage({defaultMessage: 'Filled from the description the server reports. Edit it as needed, then save.'});
    } else if (autoFill.status === 'filled') {
        status = intl.formatMessage({defaultMessage: 'The server has no description, so this was filled from its usage instructions. Edit it as needed, then save.'});
    } else if (autoFill.status === 'empty') {
        status = intl.formatMessage({defaultMessage: "This server doesn't describe itself. Write a description instead."});
    } else if (autoFill.status === 'needsOAuth') {
        status = intl.formatMessage({defaultMessage: 'Connect your account to this server, then try again.'});
    } else if (autoFill.status === 'failed') {
        status = intl.formatMessage({defaultMessage: "Couldn't get a description from this server."});
    }

    return (
        <>
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Description'})}
                placeholder={intl.formatMessage({defaultMessage: 'What this integration is for, for example: Search and update Jira issues and sprints.'})}
                value={description}
                multiline={true}
                maxLength={MAX_MCP_SERVER_DESCRIPTION_LENGTH}
                onChange={(e) => onChange(e.target.value)}
                helptext={intl.formatMessage(
                    {defaultMessage: "Tells agents what this integration is for, so they can offer it to users who haven't connected it yet. {count}/{max} characters."},
                    {count: [...description].length, max: MAX_MCP_SERVER_DESCRIPTION_LENGTH},
                )}
            />
            {!readOnly && (
                <FormRow>
                    <ItemLabel/>
                    <TextFieldContainer>
                        <AutoFillRow>
                            <Button
                                emphasis='secondary'
                                size='small'
                                disabled={!canAutoFill || autoFill.status === 'loading'}
                                onClick={fill}
                            >
                                {autoFill.status === 'loading' ? intl.formatMessage({defaultMessage: 'Fetching…'}) : intl.formatMessage({defaultMessage: 'Auto-fill from server'})}
                            </Button>
                            {autoFill.status === 'needsOAuth' && (
                                <Button
                                    emphasis='tertiary'
                                    size='small'
                                    trailingIcon={<Icon glyph={<OpenInNewIcon/>}/>}
                                    onClick={() => window.open(autoFill.authURL, '_blank', 'noopener,noreferrer')}
                                >
                                    {intl.formatMessage({defaultMessage: 'Connect'})}
                                </Button>
                            )}
                        </AutoFillRow>
                        {status && <HelpText>{status}</HelpText>}
                    </TextFieldContainer>
                </FormRow>
            )}
        </>
    );
};

const AutoFillRow = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
`;

export default MCPServerDescription;

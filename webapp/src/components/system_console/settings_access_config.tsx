// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {AdminPanel} from '@mattermost/compass-ui/components/admin-panel';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Spinner} from '@mattermost/compass-ui/components/spinner';

import {getSettingsAccess, saveSettingsAccess, type SettingsAccess} from '@/client';
import {SETTINGS_PATH} from '@/components/agents/use_agents_route';

import {SelectUsers} from '../select';

import {FormRow, HelpText, ItemLabel, ItemList, TextFieldContainer} from './item';

type Props = {
    id: string
    label: string
    helpText: React.ReactNode
    value: unknown
    disabled: boolean
    config: unknown
    currentState: unknown
    license: unknown
    setByEnv: boolean
    onChange: (id: string, value: unknown) => void
    setSaveNeeded: () => void
    registerSaveAction: (action: () => Promise<{ error?: { message?: string } }>) => void
    unRegisterSaveAction: (action: () => Promise<{ error?: { message?: string } }>) => void
}

const emptyAccess: SettingsAccess = {readWriteUserIDs: [], agentManagerUserIDs: []};

/**
 * The plugin's System Console page. Everything the plugin configures lives in
 * the Agents product's Settings view; system admins only decide here who else
 * may view (read-only) or change (read/write) it, and who may manage every
 * agent.
 */
const SettingsAccessConfig = (props: Props) => {
    const intl = useIntl();
    const [access, setAccess] = useState<SettingsAccess>(emptyAccess);
    const [loading, setLoading] = useState(true);
    const [loadError, setLoadError] = useState<string | null>(null);

    // Whether the lists were edited since they were loaded or saved. The lists
    // can only be edited once loaded, so this also means they were loaded.
    const [changed, setChanged] = useState(false);

    useEffect(() => {
        let current = true;
        getSettingsAccess().
            then((loaded) => {
                if (current) {
                    setAccess({...emptyAccess, ...loaded});
                    setLoadError(null);
                }
            }).
            catch(() => {
                if (current) {
                    setLoadError(intl.formatMessage({defaultMessage: 'Failed to load who has access to the Agents settings.'}));
                }
            }).
            finally(() => {
                if (current) {
                    setLoading(false);
                }
            });
        return () => {
            current = false;
        };
    }, [intl]);

    // The host's Save button runs this for any change on the page, Enable Plugin
    // included. It PUTs the two lists only when they were edited: lists that are
    // still loading, failed to load, or untouched would overwrite the stored ones.
    useEffect(() => {
        const save = async () => {
            if (!changed) {
                return {};
            }
            try {
                setAccess({...emptyAccess, ...await saveSettingsAccess(access)});
                setChanged(false);
                return {};
            } catch {
                return {error: {message: intl.formatMessage({defaultMessage: 'Failed to save who has access to the Agents settings.'})}};
            }
        };
        props.registerSaveAction(save);
        return () => {
            props.unRegisterSaveAction(save);
        };
    }, [access, changed, intl, props.registerSaveAction, props.unRegisterSaveAction]);

    const update = useCallback((updates: Partial<SettingsAccess>) => {
        setAccess((prev) => ({...prev, ...updates}));
        setChanged(true);
        props.setSaveNeeded();
    }, [props.setSaveNeeded]);

    // Through the host's router when it is there, so the webapp does not reload.
    const openSettings = () => {
        const history = window.WebappUtils?.browserHistory;
        if (history) {
            history.push(SETTINGS_PATH);
        } else {
            window.location.assign(SETTINGS_PATH);
        }
    };

    let lists: React.ReactNode;
    if (loading) {
        lists = (
            <LoadingContainer>
                <Spinner
                    size='32'
                    aria-label={intl.formatMessage({defaultMessage: 'Loading...'})}
                />
            </LoadingContainer>
        );
    } else if (loadError) {
        lists = (
            <SectionNotice
                type='danger'
                title={loadError}
            />
        );
    } else {
        lists = (
            <ItemList>
                <FormRow>
                    <ItemLabel>
                        <FormattedMessage defaultMessage='Read and write access'/>
                    </ItemLabel>
                    <TextFieldContainer>
                        <SelectUsers
                            userIDs={access.readWriteUserIDs}
                            onChangeUserIDs={(readWriteUserIDs) => update({readWriteUserIDs})}
                            placeholder={intl.formatMessage({defaultMessage: 'Search for people to give read and write access'})}
                        />
                        <HelpText>
                            <FormattedMessage defaultMessage='Can edit all Agents settings, API keys, and services that receive conversations (including private messages). Grant only to trusted admins.'/>
                        </HelpText>
                    </TextFieldContainer>
                </FormRow>
                <FormRow>
                    <ItemLabel>
                        <FormattedMessage defaultMessage='Agent managers'/>
                    </ItemLabel>
                    <TextFieldContainer>
                        <SelectUsers
                            userIDs={access.agentManagerUserIDs}
                            onChangeUserIDs={(agentManagerUserIDs) => update({agentManagerUserIDs})}
                            placeholder={intl.formatMessage({defaultMessage: 'Search for people to make agent managers'})}
                        />
                        <HelpText>
                            <FormattedMessage defaultMessage='Can edit and delete any agent (same as manage_others_agent). Does not grant settings access.'/>
                        </HelpText>
                    </TextFieldContainer>
                </FormRow>
            </ItemList>
        );
    }

    return (
        <Container>
            <SectionNotice
                type='info'
                title={<FormattedMessage defaultMessage='Agents settings have moved'/>}
                description={<FormattedMessage defaultMessage='Configure Agents from the Settings page in the Agents product. System admins always have access; grant access to others below.'/>}
                primaryButtonLabel={<FormattedMessage defaultMessage='Open Agents settings'/>}
                onPrimaryAction={openSettings}
            />
            <AdminPanel
                title={intl.formatMessage({defaultMessage: 'Settings access'})}
                subtitle={intl.formatMessage({defaultMessage: 'Let people who are not system admins change the Agents settings, or manage every agent.'})}
            >
                {lists}
            </AdminPanel>
        </Container>
    );
};

const Container = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xl);
`;

const LoadingContainer = styled.div`
    display: flex;
    justify-content: center;
    align-items: center;
    padding: var(--spacing-xxxxl);
`;

export default SettingsAccessConfig;

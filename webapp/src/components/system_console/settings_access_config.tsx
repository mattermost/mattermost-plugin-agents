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

const emptyAccess: SettingsAccess = {readOnlyUserIDs: [], readWriteUserIDs: []};

/**
 * The plugin's System Console page. Everything the plugin configures lives in
 * the Agents product's Settings view; system admins only decide here who else
 * may view (read-only) or change (read/write) it.
 */
const SettingsAccessConfig = (props: Props) => {
    const intl = useIntl();
    const [access, setAccess] = useState<SettingsAccess>(emptyAccess);
    const [loading, setLoading] = useState(true);
    const [loadError, setLoadError] = useState<string | null>(null);

    useEffect(() => {
        let current = true;
        getSettingsAccess().
            then((loaded) => {
                if (current) {
                    setAccess(loaded);
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

    // The host's Save button runs this; it PUTs only the two lists.
    useEffect(() => {
        const save = async () => {
            try {
                setAccess(await saveSettingsAccess(access));
                return {};
            } catch {
                return {error: {message: intl.formatMessage({defaultMessage: 'Failed to save who has access to the Agents settings.'})}};
            }
        };
        props.registerSaveAction(save);
        return () => {
            props.unRegisterSaveAction(save);
        };
    }, [access, intl, props.registerSaveAction, props.unRegisterSaveAction]);

    const update = useCallback((updates: Partial<SettingsAccess>) => {
        setAccess((prev) => ({...prev, ...updates}));
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
                            onChangeUserIDs={(readWriteUserIDs) => update({
                                readWriteUserIDs,

                                // Read/write includes read; one list per person.
                                readOnlyUserIDs: access.readOnlyUserIDs.filter((id) => !readWriteUserIDs.includes(id)),
                            })}
                            placeholder={intl.formatMessage({defaultMessage: 'Search for people to give read and write access'})}
                        />
                        <HelpText>
                            <FormattedMessage defaultMessage='These people can view and change every Agents setting, including API keys and other credentials. They cannot change who has access.'/>
                        </HelpText>
                    </TextFieldContainer>
                </FormRow>
                <FormRow>
                    <ItemLabel>
                        <FormattedMessage defaultMessage='Read-only access'/>
                    </ItemLabel>
                    <TextFieldContainer>
                        <SelectUsers
                            userIDs={access.readOnlyUserIDs}
                            onChangeUserIDs={(readOnlyUserIDs) => update({
                                readOnlyUserIDs,
                                readWriteUserIDs: access.readWriteUserIDs.filter((id) => !readOnlyUserIDs.includes(id)),
                            })}
                            placeholder={intl.formatMessage({defaultMessage: 'Search for people to give read-only access'})}
                        />
                        <HelpText>
                            <FormattedMessage defaultMessage='These people can view the Agents settings without API keys and other credentials, and cannot change them.'/>
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
                description={<FormattedMessage defaultMessage='Configure AI services, MCP servers, search and the other Agents settings from the Settings page of the Agents product. System admins can always open it; give other people access below.'/>}
                primaryButtonLabel={<FormattedMessage defaultMessage='Open Agents settings'/>}
                onPrimaryAction={openSettings}
            />
            <AdminPanel
                title={intl.formatMessage({defaultMessage: 'Settings access'})}
                subtitle={intl.formatMessage({defaultMessage: 'Let people who are not system admins view or change the Agents settings.'})}
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

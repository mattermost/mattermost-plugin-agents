// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {LockOutlineIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {EmptyState} from '@mattermost/compass-ui/components/empty-state';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {Tag} from '@mattermost/compass-ui/components/tag';
import {TextInput} from '@mattermost/compass-ui/components/text-input';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {getCustomPrompts, updateCustomPrompt} from '@/client';
import {ToggleSwitch} from '@/components/toggle_switch';
import {CustomPrompt} from '@/types';
import {MaxSkillDescriptionRunes, SkillsConfig} from '@/types/agents';

type Props = {
    skills: SkillsConfig;
    onChange: (updates: Partial<SkillsConfig>) => void;
    currentUserId: string;

    /** Whether the license allows sharing skills (shared custom prompts). */
    sharingLicensed: boolean;
}

const SkillsTab = ({skills, onChange, currentUserId, sharingLicensed}: Props) => {
    const intl = useIntl();
    const [prompts, setPrompts] = useState<CustomPrompt[]>([]);
    const [loading, setLoading] = useState(true);
    const [loadFailed, setLoadFailed] = useState(false);
    const [sharingId, setSharingId] = useState<string | null>(null);
    const [shareFailed, setShareFailed] = useState(false);

    // A share resolves after the user may have changed other settings; read the latest config then.
    const skillsRef = useRef(skills);
    skillsRef.current = skills;
    const mountedRef = useRef(true);

    useEffect(() => {
        mountedRef.current = true;
        let stale = false;
        getCustomPrompts().then((list) => {
            if (!stale) {
                setPrompts(list ?? []);
                setLoadFailed(false);
            }
        }).catch(() => {
            if (!stale) {
                setLoadFailed(true);
            }
        }).finally(() => {
            if (!stale) {
                setLoading(false);
            }
        });
        return () => {
            stale = true;
            mountedRef.current = false;
        };
    }, []);

    const sharedSkills = useMemo(() => prompts.filter((p) => p.is_shared), [prompts]);
    const personalSkills = useMemo(
        () => prompts.filter((p) => !p.is_shared && p.creator_id === currentUserId),
        [prompts, currentUserId],
    );

    const enabledSharedIDs = skills.enabledSharedIDs ?? [];
    const overrides = skills.descriptionOverrides ?? {};

    const setSharedEnabled = (id: string, enabled: boolean) => {
        const current = skillsRef.current.enabledSharedIDs ?? [];
        onChange({enabledSharedIDs: enabled ? [...current.filter((x) => x !== id), id] : current.filter((x) => x !== id)});
    };

    const setOverride = (id: string, value: string) => {
        const next = {...(skillsRef.current.descriptionOverrides ?? {})};
        if (value.trim() === '') {
            delete next[id];
        } else {
            next[id] = value;
        }
        onChange({descriptionOverrides: next});
    };

    const share = async (prompt: CustomPrompt) => {
        setSharingId(prompt.id);
        setShareFailed(false);
        try {
            // Updates replace the whole skill, so share the stored copy rather than the one loaded with the tab.
            const latest = (await getCustomPrompts()).find((p) => p.id === prompt.id);
            if (!latest) {
                throw new Error('skill not found');
            }
            await updateCustomPrompt(latest.id, {
                name: latest.name,
                description: latest.description,
                template: latest.template,
                is_shared: true,
            });
            if (!mountedRef.current) {
                return;
            }
            setPrompts((prev) => prev.map((p) => (p.id === latest.id ? {...latest, is_shared: true} : p)));
            const current = skillsRef.current.enabledSharedIDs ?? [];
            if (!current.includes(prompt.id)) {
                onChange({enabledSharedIDs: [...current, prompt.id]});
            }
        } catch {
            if (mountedRef.current) {
                setShareFailed(true);
            }
        } finally {
            if (mountedRef.current) {
                setSharingId(null);
            }
        }
    };

    const renderDetails = (prompt: CustomPrompt) => {
        const override = overrides[prompt.id] ?? '';
        return (
            <>
                {prompt.description && <SkillDescription>{prompt.description}</SkillDescription>}
                {!prompt.description && !override.trim() && (
                    <SkillHint>
                        <FormattedMessage defaultMessage='Add a description so the agent can choose this skill'/>
                    </SkillHint>
                )}
                <OverrideInput
                    id={`skill-description-override-${prompt.id}`}
                    size='small'
                    label={intl.formatMessage({defaultMessage: 'Description override'})}
                    placeholder={intl.formatMessage({defaultMessage: 'Optional: replaces the description for this agent'})}
                    value={override}
                    maxLength={MaxSkillDescriptionRunes}
                    onChange={(e) => setOverride(prompt.id, e.target.value)}
                    onBlur={(e) => setOverride(prompt.id, e.target.value.trim())}
                />
            </>
        );
    };

    let lists: React.ReactNode;
    if (loading) {
        lists = (
            <LoadingContainer>
                <Spinner size='20'/>
                <FormattedMessage defaultMessage='Loading skills...'/>
            </LoadingContainer>
        );
    } else if (loadFailed) {
        lists = (
            <SectionNotice
                type='danger'
                title={intl.formatMessage({defaultMessage: 'Failed to load skills.'})}
            />
        );
    } else if (sharedSkills.length === 0 && personalSkills.length === 0) {
        lists = (
            <EmptyState
                title={<FormattedMessage defaultMessage='No skills yet'/>}
                description={<FormattedMessage defaultMessage='Create skills from Skills in the AI actions menu of the message box.'/>}
            />
        );
    } else {
        lists = (
            <>
                {shareFailed && (
                    <div role='alert'>
                        <SectionNotice
                            type='danger'
                            title={intl.formatMessage({defaultMessage: 'Failed to share the skill. Please try again.'})}
                        />
                    </div>
                )}
                <Section aria-labelledby='skills-shared-heading'>
                    <SectionTitle id='skills-shared-heading'>
                        <FormattedMessage defaultMessage='Shared skills'/>
                    </SectionTitle>
                    {sharedSkills.length === 0 ? (
                        <SectionEmpty>
                            <FormattedMessage defaultMessage='No skills are shared yet.'/>
                        </SectionEmpty>
                    ) : (
                        <SkillList>
                            {sharedSkills.map((prompt) => {
                                const enabled = enabledSharedIDs.includes(prompt.id);
                                return (
                                    <SkillRow key={prompt.id}>
                                        <SkillHeader>
                                            <SkillName>{prompt.name}</SkillName>
                                            <ToggleSwitch
                                                size='small'
                                                ariaLabel={enabled ? intl.formatMessage(
                                                    {defaultMessage: 'Disable skill {name}'},
                                                    {name: prompt.name},
                                                ) : intl.formatMessage(
                                                    {defaultMessage: 'Enable skill {name}'},
                                                    {name: prompt.name},
                                                )}
                                                checked={enabled}
                                                onChange={(checked) => setSharedEnabled(prompt.id, checked)}
                                            />
                                        </SkillHeader>
                                        {renderDetails(prompt)}
                                    </SkillRow>
                                );
                            })}
                        </SkillList>
                    )}
                </Section>
                <Section aria-labelledby='skills-personal-heading'>
                    <SectionTitle id='skills-personal-heading'>
                        <FormattedMessage defaultMessage='Your personal skills'/>
                    </SectionTitle>
                    {personalSkills.length === 0 ? (
                        <SectionEmpty>
                            <FormattedMessage defaultMessage='You have no personal skills.'/>
                        </SectionEmpty>
                    ) : (
                        <SkillList>
                            {personalSkills.map((prompt) => (
                                <SkillRow key={prompt.id}>
                                    <SkillHeader>
                                        <SkillName>{prompt.name}</SkillName>
                                        <WithTooltip title={<FormattedMessage defaultMessage='Only you can use this skill. Share it to make it available to other users.'/>}>
                                            <Tag
                                                tabIndex={0}
                                                leadingIcon={<Icon glyph={<LockOutlineIcon/>}/>}
                                                label={<FormattedMessage defaultMessage='Personal'/>}
                                            />
                                        </WithTooltip>
                                        {sharingLicensed && (
                                            <ShareButton
                                                emphasis='tertiary'
                                                size='small'
                                                loading={sharingId === prompt.id}
                                                disabled={sharingId !== null}
                                                aria-label={intl.formatMessage({defaultMessage: 'Share skill {name}'}, {name: prompt.name})}
                                                onClick={() => share(prompt)}
                                            >
                                                <FormattedMessage defaultMessage='Share'/>
                                            </ShareButton>
                                        )}
                                    </SkillHeader>
                                    {renderDetails(prompt)}
                                </SkillRow>
                            ))}
                        </SkillList>
                    )}
                </Section>
            </>
        );
    }

    return (
        <Container>
            <ToggleRow>
                <ToggleText>
                    <ToggleTitle>
                        <FormattedMessage defaultMessage='Enable skills'/>
                    </ToggleTitle>
                    <ToggleHint>
                        <FormattedMessage defaultMessage='Skills are instructions the agent loads when a request matches their description.'/>
                    </ToggleHint>
                </ToggleText>
                <ToggleSwitch
                    ariaLabel={intl.formatMessage({defaultMessage: 'Enable skills'})}
                    checked={!skills.disabled}
                    onChange={(checked) => onChange({disabled: !checked})}
                />
            </ToggleRow>
            {!skills.disabled && (
                <>
                    <ToggleRow>
                        <ToggleText>
                            <ToggleTitle>
                                <FormattedMessage defaultMessage='Allow personal skills'/>
                            </ToggleTitle>
                            <ToggleHint>
                                <FormattedMessage defaultMessage="Each user's own skills are available to this agent in their direct messages with it."/>
                            </ToggleHint>
                        </ToggleText>
                        <ToggleSwitch
                            ariaLabel={intl.formatMessage({defaultMessage: 'Allow personal skills'})}
                            checked={skills.allowPersonal}
                            onChange={(checked) => onChange({allowPersonal: checked})}
                        />
                    </ToggleRow>
                    {lists}
                </>
            )}
        </Container>
    );
};

// --- Styled Components ---

const Container = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-l);
`;

const ToggleRow = styled.div`
    display: flex;
    align-items: flex-start;
    gap: var(--spacing-m);
`;

const ToggleText = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
    gap: var(--spacing-xxxs);
`;

const ToggleTitle = styled.span`
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    color: var(--center-channel-color);
`;

const ToggleHint = styled.span`
    font-size: var(--font-size-75);
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const Section = styled.section`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
`;

const SectionTitle = styled.h3`
    margin: 0;
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    color: var(--center-channel-color);
`;

const SectionEmpty = styled.p`
    margin: 0;
    font-size: var(--font-size-75);
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const SkillList = styled.ul`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
    margin: 0;
    padding: 0;
    list-style: none;
`;

const SkillRow = styled.li`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
    padding: var(--spacing-m) var(--spacing-l);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: var(--radius-s);
`;

const SkillHeader = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-m);
`;

const SkillName = styled.div`
    flex: 1;
    min-width: 0;
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    color: var(--center-channel-color);
    overflow-wrap: anywhere;
`;

const SkillDescription = styled.div`
    font-size: var(--font-size-75);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    overflow-wrap: anywhere;
`;

const SkillHint = styled.div`
    font-size: var(--font-size-75);
    font-style: italic;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const OverrideInput = styled(TextInput)`
    margin-top: var(--spacing-xxs);
`;

const ShareButton = styled(Button)`
    flex-shrink: 0;
`;

const LoadingContainer = styled.div`
    display: flex;
    justify-content: center;
    align-items: center;
    gap: var(--spacing-xs);
    padding: var(--spacing-xxxxl);
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

export default SkillsTab;

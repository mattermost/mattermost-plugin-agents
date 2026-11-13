// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {Renderer} from '@openuidev/react-lang';

import {Button} from '@mattermost/compass-ui/components/button';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';

import {
    BlockInteraction,
    BlockInteractionContext,
    PostInteraction,
    SubmissionField,
    SubmissionRecord,
    useGenerativeUIActions,
} from './actions';
import {mattermostLibrary} from './library';

interface Props {
    source: string;
    isStreaming: boolean;

    /** This block's index among the UI blocks of the whole reply. */
    blockIndex: number;
}

const noticeFor = (interaction: PostInteraction, isThisBlock: boolean): BlockInteraction['notice'] => {
    switch (interaction.kind) {
    case 'submitted':
        return isThisBlock ? 'submitted' : 'stale';
    case 'stale':
        return 'stale';
    case 'onlooker':
        return 'onlooker';
    case 'editable':
    case 'readonly':
        return null;
    default: {
        const unhandled: never = interaction;
        return unhandled;
    }
    }
};

// Renderer hydration shape: {form: {field: {value, componentType}}}.
const hydrationFor = (submission: SubmissionRecord | null) => {
    if (!submission || submission.form === '') {
        return undefined; // eslint-disable-line no-undefined
    }
    const fields: Record<string, {value: unknown}> = {};
    for (const field of submission.fields) {
        fields[field.name] = {value: field.value};
    }
    return {[submission.form]: fields};
};

const READ_ONLY: PostInteraction = {kind: 'readonly'};

/** Connects the block's buttons to the post's submit action and tracks the click in flight. */
const useBlockInteractionState = (blockIndex: number) => {
    const actions = useGenerativeUIActions();
    const [submittingLabel, setSubmittingLabel] = useState<string | null>(null);
    const [error, setError] = useState<string | null>(null);
    const inFlight = useRef(false);

    const interaction = actions?.interaction ?? READ_ONLY;
    const submitted = interaction.kind === 'submitted' && interaction.submission.block === blockIndex ? interaction.submission : null;

    const value = useMemo<BlockInteraction>(() => ({
        disabled: interaction.kind !== 'editable' || submittingLabel !== null,
        submittingLabel,
        submitted,
        notice: noticeFor(interaction, submitted !== null),
        requesterUsername: interaction.kind === 'onlooker' ? interaction.requesterUsername : undefined, // eslint-disable-line no-undefined
        submit: (form: string, label: string, fields: SubmissionField[]) => {
            if (!actions || inFlight.current) {
                return;
            }
            inFlight.current = true;
            setSubmittingLabel(label);
            setError(null);
            actions.submit({block: blockIndex, form, label, fields}).catch((err: unknown) => {
                setError(err instanceof Error ? err.message : String(err));
            }).finally(() => {
                inFlight.current = false;
                setSubmittingLabel(null);
            });
        },
    }), [actions, interaction, submitted, submittingLabel, blockIndex]);

    return {value, error, hydration: hydrationFor(submitted)};
};

/**
 * Renders one OpenUI Lang program with the Mattermost component library. Only
 * library components can render; anything the parser cannot map is dropped.
 */
const GenerativeUIBlock = ({source, isStreaming, blockIndex}: Props) => {
    const [showSource, setShowSource] = useState(false);
    const interaction = useBlockInteractionState(blockIndex);

    return (
        <Block
            data-testid='generative-ui-block'
            aria-busy={interaction.value.submittingLabel !== null}
        >
            <RenderBoundary source={source}>
                <BlockInteractionContext.Provider value={interaction.value}>
                    <Renderer
                        response={source}
                        library={mattermostLibrary}
                        isStreaming={isStreaming}
                        initialState={interaction.hydration}
                    />
                </BlockInteractionContext.Provider>
            </RenderBoundary>
            {interaction.error && (
                <SectionNotice
                    type='danger'
                    title={
                        <FormattedMessage
                            id='ai.generative_ui.submit_failed'
                            defaultMessage='Could not send your response'
                        />
                    }
                    description={interaction.error}
                />
            )}
            {!isStreaming && (
                <SourceToggle>
                    <Button
                        emphasis='link'
                        size='x-small'
                        onClick={() => setShowSource((prev) => !prev)}
                        aria-expanded={showSource}
                    >
                        {showSource ? (
                            <FormattedMessage
                                id='ai.generative_ui.hide_source'
                                defaultMessage='Hide source'
                            />
                        ) : (
                            <FormattedMessage
                                id='ai.generative_ui.view_source'
                                defaultMessage='View source'
                            />
                        )}
                    </Button>
                </SourceToggle>
            )}
            {showSource && <Source>{source}</Source>}
        </Block>
    );
};

interface BoundaryProps {
    source: string;
    children: React.ReactNode;
}

// A component that throws on malformed props falls back to the raw program
// instead of taking down the whole post.
class RenderBoundary extends React.Component<BoundaryProps, {failed: boolean}> {
    state = {failed: false};

    static getDerivedStateFromError() {
        return {failed: true};
    }

    componentDidUpdate(prevProps: BoundaryProps) {
        if (this.state.failed && prevProps.source !== this.props.source) {
            this.setState({failed: false});
        }
    }

    render() {
        if (this.state.failed) {
            return <Source>{this.props.source}</Source>;
        }
        return this.props.children;
    }
}

const Block = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxs);
    margin: var(--spacing-xs) 0;
    max-width: 720px;
`;

const SourceToggle = styled.div`
    display: flex;
`;

const Source = styled.pre`
    margin: 0;
    padding: var(--spacing-xs) var(--spacing-m);
    border-radius: var(--radius-s);
    background: rgba(var(--center-channel-color-rgb), 0.04);
    font-size: 12px;
    line-height: 16px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
`;

export default GenerativeUIBlock;

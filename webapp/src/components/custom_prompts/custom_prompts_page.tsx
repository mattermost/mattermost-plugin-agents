// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {ArrowLeftIcon} from '@mattermost/compass-icons/components';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

import CustomPromptsContent from './custom_prompts_content';

/**
 * Custom Prompts as a product page (the `prompts` view of the Agents product).
 * Same content and behavior as the modal, under page chrome. Fills the product
 * content outlet: the outlet is a flex column with min-height: 0, so this page
 * takes the remaining height and the list/form scroll inside it.
 */
const CustomPromptsPage = () => {
    const intl = useIntl();

    return (
        <CustomPromptsContent
            active={true}
            layout='page'
        >
            {({title, showBack, onBack, createAction, body}) => (
                <PageContainer>
                    <ContentColumn>
                        <PageHeader>
                            <HeaderLeading>
                                {showBack && (
                                    <BackButton
                                        icon={<Icon glyph={<ArrowLeftIcon/>}/>}
                                        size='medium'
                                        onClick={onBack}
                                        aria-label={intl.formatMessage({defaultMessage: 'Back to prompts'})}
                                    />
                                )}
                                <TitleBlock>
                                    <PageTitle>{title}</PageTitle>
                                    {!showBack && (
                                        <Subtitle>
                                            <FormattedMessage defaultMessage='Reusable prompts you can run from the message composer in any channel.'/>
                                        </Subtitle>
                                    )}
                                </TitleBlock>
                            </HeaderLeading>
                            {createAction && <HeaderActions>{createAction}</HeaderActions>}
                        </PageHeader>
                        <PageBody>{body}</PageBody>
                    </ContentColumn>
                </PageContainer>
            )}
        </CustomPromptsContent>
    );
};

// Same column as the All agents view: 960px max, centered, xxxl side padding.
const CONTENT_MAX_WIDTH = '960px';

const PageContainer = styled.div`
    display: flex;
    flex: 1 1 0;
    flex-direction: column;
    min-height: 0;
    width: 100%;
`;

const ContentColumn = styled.div`
    display: flex;
    flex: 1 1 0;
    flex-direction: column;
    min-height: 0;
    width: 100%;
    max-width: ${CONTENT_MAX_WIDTH};
    margin: 0 auto;
    padding: 0 var(--spacing-xxxl);
`;

const PageHeader = styled.div`
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: space-between;
    gap: var(--spacing-l);
    min-width: 0;
    padding: var(--spacing-xxxxxl) 0 var(--spacing-xxl);
`;

const HeaderLeading = styled.div`
    display: flex;
    flex: 1;
    align-items: center;
    gap: var(--spacing-xs);
    min-width: 0;
`;

const TitleBlock = styled.div`
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--spacing-xxxs);
    min-width: 0;
`;

const HeaderActions = styled.div`
    display: flex;
    flex-shrink: 0;
    align-items: center;
    gap: var(--spacing-xs);
`;

const PageTitle = styled.h1`
    margin: 0;
    max-width: 100%;
    overflow: hidden;
    font-family: var(--font-family-heading, 'Metropolis', sans-serif);
    font-size: var(--font-size-500);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-500);
    color: var(--center-channel-color);
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const Subtitle = styled.p`
    margin: 0;
    font-family: var(--font-family-body, 'Open Sans', sans-serif);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const BackButton = styled(IconButton)`
    flex-shrink: 0;
    margin-left: calc(-1 * var(--spacing-xs));
`;

// Definite-height flex parent for the content's height: 100% sheet.
const PageBody = styled.div`
    display: flex;
    flex: 1 1 0;
    flex-direction: column;
    min-height: 0;
`;

export default CustomPromptsPage;

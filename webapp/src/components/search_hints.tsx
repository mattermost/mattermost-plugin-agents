// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {ChevronDownIcon, LightbulbOutlineIcon} from '@mattermost/compass-icons/components';
import {useSelector} from 'react-redux';
import {GlobalState} from '@mattermost/types/store';

import {useBotlist} from '@/bots';
import {useIsLicensedFor} from '@/license';
import manifest from '../manifest';

import {BotDropdown} from './bot_selector';

const SearchHintsContainer = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 20px 28px;
    border-radius: 4px;
`;

const HintContent = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
`;

const LightbulbIcon = styled(LightbulbOutlineIcon)`
    color: rgba(var(--center-channel-text-rgb), 0.64);
    font-size: 18px;
`;

const HintText = styled.span`
    color: rgba(var(--center-channel-text-rgb), 0.64);
    font-size: 14px;
`;

const SearchWithContainer = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
`;

const SearchWithLabel = styled.span`
    color: rgba(var(--center-channel-text-rgb), 0.64);
    font-size: 11px;
    font-weight: 600;
	line-height: 16px;
	letter-spacing: 0.22px;
	text-transform: uppercase;
`;

const SelectorContainer = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	padding: 2px 4px 2px 6px;
	border-radius: 4px;
	background: rgba(var(--center-channel-color-rgb), 0.08);
`;

const SearchHints = () => {
    const {bots, activeBot, setActiveBot} = useBotlist();
    const currentBotName = activeBot?.displayName ?? '';
    const searchEnabled = useSelector<GlobalState, boolean>((state: any) => state['plugins-' + manifest.id].searchEnabled);
    const semanticSearchLicensed = useIsLicensedFor('semantic_search');

    // Don't show if search is disabled or no bots are available
    if (!searchEnabled || !semanticSearchLicensed || !bots || bots.length === 0) {
        return null;
    }

    return (
        <SearchHintsContainer>
            <HintContent>
                <LightbulbIcon/>
                <HintText>
                    <FormattedMessage defaultMessage='Agents searches only content you have access to'/>
                </HintText>
            </HintContent>
            <SearchWithContainer>
                <SearchWithLabel>
                    <FormattedMessage defaultMessage='SEARCH WITH'/>
                </SearchWithLabel>
                <BotDropdown
                    bots={bots}
                    activeBot={activeBot}
                    setActiveBot={setActiveBot}
                    container={SelectorContainer}
                >
                    {currentBotName}
                    <ChevronDownIcon/>
                </BotDropdown>
            </SearchWithContainer>
        </SearchHintsContainer>
    );
};

export default SearchHints;

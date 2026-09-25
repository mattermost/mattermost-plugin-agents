// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {useSelector} from 'react-redux';
import {GlobalState} from '@mattermost/types/store';

import IconAI from '@/components/assets/icon_ai';
import {useIsLicensedFor} from '@/license';
import manifest from '../manifest';

const SearchButtonContainer = styled.span`
    display: inline-flex;
    align-items: center;
    justify-content: center;
	gap: 6px;
`;

const StyledIconAI = styled(IconAI)`
	width: 12px;
	font-size: 12px;
`;

const SearchButton = () => {
    const searchEnabled = useSelector<GlobalState, boolean>((state: any) => state['plugins-' + manifest.id].searchEnabled);
    const semanticSearchLicensed = useIsLicensedFor('semantic_search');

    if (!searchEnabled || !semanticSearchLicensed) {
        return null;
    }
    return (
        <SearchButtonContainer>
            <StyledIconAI/>{'Agents'}
        </SearchButtonContainer>
    );
};

export default SearchButton;

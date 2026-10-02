// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

//eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip} from 'react-bootstrap';

import {Tag} from '@mattermost/compass-ui/components/tag';

import {Capability, requiredLevelFor, useLicenseLevelName} from '@/license';

// OverlayTrigger attaches its hover handlers to this element, so it must be a DOM node.
const ChipAnchor = styled.span`
    display: inline-flex;
    margin-left: 8px;
    white-space: nowrap;
`;

const MainText = styled.div`
	font-size: 12px;
	font-weight: 600;
	line-height: 15px;
`;

const SubText = styled.div`
	font-size: 11px;
	font-weight: 600;
	line-height: 16px;
	letter-spacing: 0.22px;
	opacity: 0.56;
`;

type Props = {
    subtext?: string;
    text?: string;

    // title overrides the tooltip heading. Defaults to 'Enterprise feature';
    // pass a plan-neutral heading for features available on lower paid tiers.
    title?: string;
};

const EnterpriseChip = (props: Props) => {
    return (
        <OverlayTrigger
            placement='top'
            overlay={
                <Tooltip>
                    <MainText>{props.title || 'Enterprise feature'}</MainText>
                    <SubText>{props.subtext}</SubText>
                </Tooltip>
            }
        >
            <ChipAnchor>
                <Tag
                    type='info'
                    label={props.text || 'Enterprise'}
                />
            </ChipAnchor>
        </OverlayTrigger>
    );
};

// useLicenseChipProps names the required plan for a capability. Pass the
// result to EnterpriseChip so admin surfaces stay consistent.
export function useLicenseChipProps(capability: Capability): {title: string; text: string; subtext: string; levelName: string} {
    const intl = useIntl();
    const levelName = useLicenseLevelName();
    const name = levelName(requiredLevelFor(capability));
    const available = intl.formatMessage(
        {defaultMessage: 'Available on {level} plans and above'},
        {level: name},
    );
    return {
        title: available,
        text: name,
        subtext: available,
        levelName: name,
    };
}

// LicenseChip marks a control as unavailable at the current license level and
// names the plan that provides capability.
export const LicenseChip = ({capability}: {capability: Capability}) => {
    const chip = useLicenseChipProps(capability);
    return (
        <EnterpriseChip
            title={chip.title}
            text={chip.text}
            subtext={chip.subtext}
        />
    );
};

export default EnterpriseChip;

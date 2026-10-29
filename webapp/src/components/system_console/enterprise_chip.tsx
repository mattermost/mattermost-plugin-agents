// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {Tag} from '@mattermost/compass-ui/components/tag';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {Capability, requiredLevelFor, useLicenseLevelName} from '@/license';

const ChipAnchor = styled.span`
    display: inline-flex;
    margin-left: 8px;
    white-space: nowrap;
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
        <WithTooltip
            title={props.title || 'Enterprise feature'}
            hint={props.subtext}
        >
            <ChipAnchor>
                <Tag
                    type='info'
                    label={props.text || 'Enterprise'}
                />
            </ChipAnchor>
        </WithTooltip>
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

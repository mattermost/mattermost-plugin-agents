// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import {EnabledTool} from '@/types/agents';

import MCPToolsPicker from './mcp_tools_picker';

type Props = {
    enabledTools: EnabledTool[];
    autoEnableNewMCPTools: boolean;
    onChange: (updates: {enabledTools?: EnabledTool[]; autoEnableNewMCPTools?: boolean}) => void;
}

const McpsTab = (props: Props) => {
    const {enabledTools, autoEnableNewMCPTools, onChange} = props;
    return (
        <MCPToolsPicker
            enabledTools={enabledTools}
            autoEnableNewMCPTools={autoEnableNewMCPTools}
            onChange={onChange}
        />
    );
};

export default McpsTab;

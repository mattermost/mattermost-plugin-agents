import fs from 'fs';
import path from 'path';

import { AIMockContainer, RunAIMockSidecar } from './aimock-container';
import { AIMockFixtureFile } from './aimock-fixtures';
import MattermostContainer from './mmcontainer';
import { RunAIMockContainer } from './plugincontainer';

export type AIMockHarness = {
    mattermost: MattermostContainer;
    aimock: AIMockContainer;
    stop: () => Promise<void>;
};

export function loadAimockFixtureFile(fixtureRelativePath: string): AIMockFixtureFile {
    const fixturePath = path.join(__dirname, '..', 'fixtures', 'aimock', fixtureRelativePath);
    const raw = fs.readFileSync(fixturePath, 'utf-8');
    return JSON.parse(raw) as AIMockFixtureFile;
}

export async function RunAIMockHarness(options: {
    fixtureFile: string;
    bot?: Partial<Record<string, unknown>>;
    service?: Partial<Record<string, unknown>>;
}): Promise<AIMockHarness> {
    const fixtures = loadAimockFixtureFile(options.fixtureFile);
    const mattermost = await RunAIMockContainer({
        bot: options.bot,
        service: options.service,
    });
    const aimock = await RunAIMockSidecar(mattermost.network, {
        fixtures,
    });

    return {
        mattermost,
        aimock,
        stop: async () => {
            await aimock.stop();
            await mattermost.stop();
        },
    };
}

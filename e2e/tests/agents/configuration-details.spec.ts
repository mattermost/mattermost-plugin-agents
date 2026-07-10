// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createHash} from 'crypto';

import {test, expect, Page} from '@playwright/test';

import {
    AgentAPIHelper,
    AgentResponse,
    ChannelAccessLevel,
    UserAccessLevel,
} from 'helpers/agent-api';
import {
    agentAdminPassword,
    agentAdminUsername,
    agentRegularPassword,
    agentRegularUsername,
    mockServiceId,
    RunAgentContainer as runAgentContainer,
} from 'helpers/agent-container';
import {AgentPageHelper} from 'helpers/agent-page';
import {MattermostPage} from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';

const maxToolTurnsValidation = 'Max tool turns must be between 1 and 250';
const profileImageSignature = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
const avatarPngBase64 = 'iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAYAAACqaXHeAAAAfUlEQVR4nO3SIREAIBAAwU9CFQQF0KSnyRMCwc+w6uyJjd1W9jHz10aFiZeNChMEEEAAAQQQQAABBBBAAAEEEEAAAQQQQAABBBBAAAEEEEAAAQQQQAABBBBAAAEEEEAAAQQQQAABBBBAAAEEEEAAAQQQQAABBBBAAAEEXPYAQZVB8O+uavkAAAAASUVORK5CYII=';
const avatarPng = Buffer.from(avatarPngBase64, 'base64');

let mattermost: MattermostContainer;
let agentApi: AgentAPIHelper;
let adminToken: string;
const agentsToDelete = new Set<string>();

function sha256(bytes: Buffer): string {
    return createHash('sha256').update(bytes).digest('hex');
}

function expectPngHeader(bytes: Buffer): {width: number; height: number} {
    expect(bytes.length).toBeGreaterThan(24);
    expect(bytes.subarray(0, profileImageSignature.length)).toEqual(profileImageSignature);
    expect(bytes.subarray(12, 16).toString('ascii')).toBe('IHDR');
    const width = bytes.readUInt32BE(16);
    const height = bytes.readUInt32BE(20);
    expect(width).toBeGreaterThan(0);
    expect(height).toBeGreaterThan(0);
    return {width, height};
}

async function fetchProfileImage(
    userId: string,
    lastPictureUpdate: number,
): Promise<Buffer> {
    const response = await fetch(
        `${mattermost.url()}/api/v4/users/${userId}/image?_=${lastPictureUpdate}`,
        {
            headers: {
                Authorization: `Bearer ${adminToken}`,
                Accept: 'image/png',
            },
        },
    );
    expect(response.status).toBe(200);

    const contentType = response.headers.get('content-type') ?? '';
    expect(contentType).toMatch(/^image\/png(?:;|$)/);
    const bytes = Buffer.from(await response.arrayBuffer());
    expectPngHeader(bytes);
    return bytes;
}

async function fetchBrowserImage(page: Page, imageUrl: string): Promise<Buffer> {
    const loaded = await page.evaluate(async (url) => {
        const response = await fetch(url, {cache: 'no-store'});
        return {
            status: response.status,
            contentType: response.headers.get('content-type') ?? '',
            bytes: Array.from(new Uint8Array(await response.arrayBuffer())),
        };
    }, imageUrl);
    expect(loaded.status).toBe(200);
    expect(loaded.contentType).toMatch(/^image\/png(?:;|$)/);
    const bytes = Buffer.from(loaded.bytes);
    expectPngHeader(bytes);
    return bytes;
}

function expectCompleteAgentUpdate(
    actual: AgentResponse,
    previous: AgentResponse,
    overrides: Partial<AgentResponse> = {},
): void {
    expect(actual).toEqual({
        ...previous,
        ...overrides,
        updateAt: actual.updateAt,
    });
    expect(actual.updateAt).toEqual(expect.any(Number));
    expect(previous.updateAt).toEqual(expect.any(Number));
    expect(actual.updateAt as number).toBeGreaterThan(previous.updateAt as number);
}

async function reopenAgent(
    page: Page,
    agentPage: AgentPageHelper,
    displayName: string,
): Promise<void> {
    await page.reload();
    await agentPage.waitForAgentsLoaded();
    await agentPage.openAgentActions(displayName);
    await agentPage.clickEditAction(displayName);
    await agentPage.waitForModal();
}

async function saveMaxToolTurnsAndReopen(
    page: Page,
    agentPage: AgentPageHelper,
    agentId: string,
    displayName: string,
    username: string,
    previous: AgentResponse,
    maxToolTurns: number,
): Promise<AgentResponse> {
    await agentPage.getMaxToolTurnsInput().fill(String(maxToolTurns));
    const updateResponse = page.waitForResponse((response) => {
        const url = new URL(response.url());
        return url.pathname === `/plugins/mattermost-ai/agents/${agentId}` &&
            response.request().method() === 'PUT';
    });
    await agentPage.getModalSaveButton().click();
    expect((await updateResponse).status()).toBe(200);
    await agentPage.waitForModalClosed();

    const saved = await agentApi.getAgent(adminToken, agentId);
    expectCompleteAgentUpdate(saved, previous, {maxToolTurns});

    await reopenAgent(page, agentPage, displayName);
    await expect(agentPage.getUsernameInput()).toBeDisabled();
    await expect(agentPage.getUsernameInput()).toHaveValue(username);
    await expect(agentPage.getMaxToolTurnsInput()).toHaveValue(String(maxToolTurns));
    return saved;
}

test.describe('Agent configuration details', () => {
    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await runAgentContainer();
        agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        adminToken = adminClient.getToken();
    });

    test.afterEach(async () => {
        const agentIds = [...agentsToDelete];
        await Promise.all(agentIds.map((agentId) => agentApi.deleteAgent(adminToken, agentId)));
        agentsToDelete.clear();
    });

    test.afterAll(async () => {
        await mattermost?.stop();
    });

    test('locks username and persists validated max tool turns without replacing unrelated fields', async ({page}) => {
        test.setTimeout(90000);

        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const regularClient = await mattermost.getClient(agentRegularUsername, agentRegularPassword);
        const regularUser = await regularClient.getMe();
        const teams = await adminClient.getMyTeams();
        const testTeam = teams.find((team) => team.name === 'test');
        if (!testTeam) {
            throw new Error('test team not found');
        }

        const displayName = 'Configuration Details Agent';
        const username = 'configurationdetails';
        const enabledMCPTools = [
            {server_origin: 'embedded://mattermost', tool_name: 'read_post'},
        ];
        const created = await agentApi.createTestAgent(adminToken, {
            displayName,
            username,
            serviceID: mockServiceId,
            customInstructions: 'Preserve every unrelated configuration field.',
            channelAccessLevel: ChannelAccessLevel.None,
            channelIDs: [],
            userAccessLevel: UserAccessLevel.Allow,
            userIDs: [regularUser.id],
            teamIDs: [testTeam.id],
            adminUserIDs: [regularUser.id],
            enabledMCPTools,
            autoEnableNewMCPTools: false,
            mcpDynamicToolLoading: false,
            enabledNativeTools: ['web_search'],
            model: 'gpt-configuration-details',
            enableVision: false,
            disableTools: false,
            reasoningEnabled: false,
            reasoningEffort: 'low',
            thinkingBudget: 321,
            structuredOutputEnabled: false,
            maxToolTurns: 17,
        });
        agentsToDelete.add(created.id);
        const baseline = await agentApi.getAgent(adminToken, created.id);

        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);
        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());
        await agentPage.openAgentActions(displayName);
        await agentPage.clickEditAction(displayName);
        await agentPage.waitForModal();

        await expect(agentPage.getUsernameInput()).toBeDisabled();
        await expect(agentPage.getUsernameInput()).toHaveValue(username);
        await expect(agentPage.getMaxToolTurnsInput()).toHaveValue('17');

        let agentPutRequests = 0;
        page.on('request', (request) => {
            const url = new URL(request.url());
            if (url.pathname === `/plugins/mattermost-ai/agents/${created.id}` && request.method() === 'PUT') {
                agentPutRequests++;
            }
        });

        await agentPage.getMaxToolTurnsInput().fill('0');
        await agentPage.getModalSaveButton().click();
        await expect(page.getByText(maxToolTurnsValidation, {exact: true})).toBeVisible();
        await expect(agentPage.getBackButton()).toBeVisible();
        await expect(agentPage.getMaxToolTurnsInput()).toHaveValue('0');
        expect(agentPutRequests).toBe(0);
        expect(await agentApi.getAgent(adminToken, created.id)).toEqual(baseline);

        await agentPage.getMaxToolTurnsInput().fill('251');
        await agentPage.getModalSaveButton().click();
        await expect(page.getByText(maxToolTurnsValidation, {exact: true})).toBeVisible();
        await expect(agentPage.getBackButton()).toBeVisible();
        await expect(agentPage.getMaxToolTurnsInput()).toHaveValue('251');
        expect(agentPutRequests).toBe(0);
        expect(await agentApi.getAgent(adminToken, created.id)).toEqual(baseline);

        const savedMinimum = await saveMaxToolTurnsAndReopen(
            page,
            agentPage,
            created.id,
            displayName,
            username,
            baseline,
            1,
        );
        const savedMaximum = await saveMaxToolTurnsAndReopen(
            page,
            agentPage,
            created.id,
            displayName,
            username,
            savedMinimum,
            250,
        );
        await saveMaxToolTurnsAndReopen(
            page,
            agentPage,
            created.id,
            displayName,
            username,
            savedMaximum,
            42,
        );
    });

    test('uploads and persists a real agent avatar through the editor', async ({page}) => {
        test.setTimeout(90000);

        const displayName = 'Avatar Details Agent';
        const username = 'avatardetailsagent';
        const created = await agentApi.createTestAgent(adminToken, {
            displayName,
            username,
            serviceID: mockServiceId,
            customInstructions: 'Keep this configuration while the avatar changes.',
            autoEnableNewMCPTools: false,
            mcpDynamicToolLoading: false,
            enabledMCPTools: [
                {server_origin: 'embedded://mattermost', tool_name: 'read_post'},
            ],
            enabledNativeTools: [],
            model: 'gpt-avatar-details',
            enableVision: false,
            disableTools: false,
            reasoningEnabled: false,
            reasoningEffort: 'low',
            thinkingBudget: 123,
            structuredOutputEnabled: false,
            maxToolTurns: 42,
        });
        agentsToDelete.add(created.id);
        const baseline = await agentApi.getAgent(adminToken, created.id);
        if (!created.botUserID) {
            throw new Error('created agent did not include a bot user ID');
        }

        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const botBefore = await adminClient.getUserByUsername(username);
        expect(botBefore.id).toBe(created.botUserID);
        const beforeImage = await fetchProfileImage(
            botBefore.id,
            botBefore.last_picture_update,
        );

        const fixtureDimensions = expectPngHeader(avatarPng);
        expect(fixtureDimensions).toEqual({width: 64, height: 64});
        expect(sha256(avatarPng)).not.toBe(sha256(beforeImage));

        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);
        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());
        await agentPage.openAgentActions(displayName);
        await agentPage.clickEditAction(displayName);
        await agentPage.waitForModal();

        await expect(agentPage.getAvatarImage()).toHaveAttribute(
            'src',
            new RegExp(`/api/v4/users/${botBefore.id}/image`),
        );
        await agentPage.getAvatarInput().setInputFiles({
            name: 'deterministic-agent-avatar.png',
            mimeType: 'image/png',
            buffer: avatarPng,
        });
        await expect(agentPage.getAvatarImage()).toHaveAttribute('src', /^blob:/);
        await expect.poll(
            () => agentPage.getAvatarImage().evaluate((image: HTMLImageElement) => (
                image.complete ? {width: image.naturalWidth, height: image.naturalHeight} : null
            )),
        ).toEqual({width: 64, height: 64});

        await agentPage.getBackButton().click();
        await expect(agentPage.getDiscardChangesDialog()).toBeVisible();
        await agentPage.getDiscardChangesKeepEditingButton().click();
        await expect(agentPage.getDiscardChangesDialog()).not.toBeVisible();
        await expect(agentPage.getAvatarImage()).toHaveAttribute('src', /^blob:/);

        const agentUpdateResponse = page.waitForResponse((response) => {
            const url = new URL(response.url());
            return url.pathname === `/plugins/mattermost-ai/agents/${created.id}` &&
                response.request().method() === 'PUT';
        });
        const avatarUploadResponse = page.waitForResponse((response) => {
            const url = new URL(response.url());
            return url.pathname === `/plugins/mattermost-ai/agents/${created.id}/avatar` &&
                response.request().method() === 'POST';
        });
        await agentPage.getModalSaveButton().click();

        const [updateResponse, uploadResponse] = await Promise.all([
            agentUpdateResponse,
            avatarUploadResponse,
        ]);
        expect(updateResponse.status()).toBe(200);
        expect(uploadResponse.status()).toBe(200);
        await agentPage.waitForModalClosed();

        const savedAgent = await agentApi.getAgent(adminToken, created.id);
        expectCompleteAgentUpdate(savedAgent, baseline);

        const botAfter = await adminClient.getUserByUsername(username);
        expect(botAfter.id).toBe(botBefore.id);
        expect(botAfter.last_picture_update).toBeGreaterThan(botBefore.last_picture_update);
        const afterImage = await fetchProfileImage(
            botAfter.id,
            botAfter.last_picture_update,
        );
        expect(sha256(afterImage)).not.toBe(sha256(beforeImage));

        await reopenAgent(page, agentPage, displayName);
        const botReopened = await adminClient.getUserByUsername(username);
        expect(botReopened.last_picture_update).toBe(botAfter.last_picture_update);
        const reopenedAvatar = agentPage.getAvatarImage();
        await expect(reopenedAvatar).toHaveAttribute('src', new RegExp(`/api/v4/users/${botAfter.id}/image`));
        const browserImageUrl = await reopenedAvatar.getAttribute('src');
        if (!browserImageUrl) {
            throw new Error('reopened avatar did not have an image URL');
        }
        const parsedBrowserImageUrl = new URL(browserImageUrl, mattermost.url());
        expect(parsedBrowserImageUrl.pathname).toBe(`/api/v4/users/${botAfter.id}/image`);
        expect(parsedBrowserImageUrl.searchParams.get('_')).toBe(String(botAfter.last_picture_update));
        const browserLoadedImage = await fetchBrowserImage(page, parsedBrowserImageUrl.toString());
        expect(sha256(browserLoadedImage)).toBe(sha256(afterImage));
        await expect.poll(
            () => reopenedAvatar.evaluate((image: HTMLImageElement) => (
                image.complete ? image.naturalWidth : 0
            )),
        ).toBeGreaterThan(0);

        expect(await agentApi.getAgent(adminToken, created.id)).toEqual(savedAgent);
    });
});

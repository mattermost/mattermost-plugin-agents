// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, screen, fireEvent, waitFor} from '@testing-library/react';
import {Provider} from 'react-redux';
import {IntlProvider} from 'react-intl';

import {getChannelById, getPost, getProfilesByIds} from '@/client';

import {JSONValue, ToolCall, ToolCallStatus} from '../tool_types';

import {renderToolCall, ToolRenderContext} from './registry';

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('@/client', () => ({
    getPost: jest.fn(),
    getProfilesByIds: jest.fn(() => Promise.resolve([])),
    getChannelById: jest.fn(),
}));

const postMessagePreviewMock = jest.fn<React.ReactElement, [unknown]>(() => <div>{'post-preview-box'}</div>);

jest.mock('@/mm_webapp', () => ({
    PostMessagePreview: (props: unknown) => postMessagePreviewMock(props),
}));

const mockGetPost = getPost as jest.Mock;
const mockGetProfilesByIds = getProfilesByIds as jest.Mock;
const mockGetChannelById = getChannelById as jest.Mock;

// Membership tools take opaque 26-character IDs. These fixtures give each ID a
// distinctive readable identity so a rendering can be checked against it.
const memberUserId = 'uzq4m8hd3r7nf2xk6bw9ct5ya1';
const secondMemberUserId = 'p9jv2sl6ea4tq8xn3ymzd7kr5b';
const memberChannelId = 'h4wc7ktg1ms9ybn6pz2ax8djr3';

const memberProfiles: {[id: string]: {id: string; username: string}} = {
    [memberUserId]: {id: memberUserId, username: 'dana.holloway'},
    [secondMemberUserId]: {id: secondMemberUserId, username: 'omar.reyes'},
};

const memberChannel = {id: memberChannelId, display_name: 'Quarterly Planning', name: 'quarterly-planning', team_id: 'team1', type: 'P'};

// Deliberately kept out of the store below: an approver holds no entry for a
// person or channel they have no shared history with, so these exercise the
// fetch route rather than the store route.
const fetchOnlyUserId = 'k3nf7yq2hd8xr5wb1mzc6ta4pj';
const fetchOnlyChannelId = 'r6dx9bq3na7ks2yw5hmc8tfz1p';
const fetchOnlyChannel = {id: fetchOnlyChannelId, display_name: 'Incident Response', name: 'incident-response', team_id: 'team1', type: 'P'};

// Answered by neither the store nor the fetch.
const unnamedUserId = 'z8mr4kp7wc2hy6nv3sqb9dtx5f';
const unresolvableChannelId = 'w2ph5nk8bd4rt9xm3cyz6qfa7j';

const directoryProfiles: {[id: string]: {id: string; username: string}} = {
    ...memberProfiles,
    [fetchOnlyUserId]: {id: fetchOnlyUserId, username: 'lena.whitfield'},
};

const directoryChannels: {[id: string]: typeof memberChannel} = {
    [memberChannelId]: memberChannel,
    [fetchOnlyChannelId]: fetchOnlyChannel,
};

const state = {
    entities: {
        general: {config: {SiteURL: 'http://localhost:8065'}},
        teams: {currentTeamId: 'team1', teams: {team1: {id: 'team1', display_name: 'Eng', name: 'eng'}}},
        channels: {
            channels: {
                chan1: {id: 'chan1', display_name: 'Town Square', team_id: 'team1', type: 'O'},
                [memberChannelId]: memberChannel,
            },
        },
        users: {
            currentUserId: 'u-self',
            profiles: {
                'u-self': {id: 'u-self', username: 'requester'},
                ...memberProfiles,
            },
        },
        posts: {posts: {}},
    },
};

const store = {
    getState: () => state,
    subscribe: () => jest.fn(),
    dispatch: jest.fn(),
} as any;

beforeEach(() => {
    mockGetPost.mockReset();
    postMessagePreviewMock.mockClear();
});

function makeCtx(tool: ToolCall): ToolRenderContext {
    return {
        tool,
        isCollapsed: false,
        isProcessing: false,
        onToggleCollapse: jest.fn(),
        canExpand: true,
        showArguments: true,
        showResults: false,
        approvalStage: 'call',
        isAutoApproved: false,
        canAnswer: true,
    };
}

function renderTool(tool: ToolCall) {
    return render(
        <Provider store={store}>
            <IntlProvider locale='en'>
                {renderToolCall(makeCtx(tool))}
            </IntlProvider>
        </Provider>,
    );
}

function makeTool(overrides: Partial<ToolCall>): ToolCall {
    return {
        id: 'tc1',
        name: 'test_tool',
        description: '',
        status: ToolCallStatus.Pending,
        ...overrides,
    };
}

describe('renderToolCall routing', () => {
    test('routes a valid select question to QuestionCard', () => {
        renderTool(makeTool({
            name: 'AskUserQuestion',
            user_interaction: 'select',
            arguments: {question: 'Pick one', options: [{label: 'A'}, {label: 'B'}]},
        }));

        expect(screen.getByText('Pick one')).not.toBeNull();
    });

    test('a redacted select (null args) falls back to the generic card', () => {
        renderTool(makeTool({
            name: 'AskUserQuestion',
            user_interaction: 'select',
            arguments: undefined, // eslint-disable-line no-undefined
        }));

        expect(screen.getByText('AskUserQuestion')).not.toBeNull();
        expect(screen.queryByText('Pick one')).toBeNull();
    });

    test('routes an embedded read_post to the post preview card', async () => {
        mockGetPost.mockResolvedValue({id: 'p1', user_id: 'u1', channel_id: 'chan1', message: 'previewed message'});

        renderTool(makeTool({
            name: 'mattermost__read_post',
            mcp_bare_name: 'read_post',
            server_origin: 'embedded://mattermost',
            arguments: {post_id: 'p1'},
        }));

        await waitFor(() => expect(screen.getByText('post-preview-box')).not.toBeNull());
        expect(mockGetPost).toHaveBeenCalledWith('p1');
    });

    test('read_post falls back to the generic field list when the post fetch fails', async () => {
        mockGetPost.mockRejectedValue(new Error('gone'));

        renderTool(makeTool({
            name: 'mattermost__read_post',
            mcp_bare_name: 'read_post',
            server_origin: 'embedded://mattermost',
            arguments: {post_id: 'p-missing'},
        }));

        await waitFor(() => expect(screen.getByText('Post Id')).not.toBeNull());
        expect(screen.getByText('p-missing')).not.toBeNull();
        expect(screen.queryByText('post-preview-box')).toBeNull();
    });

    test('an executed read_post renders generically — no preview, no post fetch', () => {
        renderTool(makeTool({
            name: 'mattermost__read_post',
            mcp_bare_name: 'read_post',
            server_origin: 'embedded://mattermost',
            status: ToolCallStatus.Success,
            arguments: {post_id: 'p1'},
            result: 'post content',
        }));

        expect(mockGetPost).not.toHaveBeenCalled();
        expect(screen.queryByText('post-preview-box')).toBeNull();
        expect(screen.getByText('Post Id')).not.toBeNull();
    });

    test('a read_post-named tool from an EXTERNAL server does not get the preview card', () => {
        renderTool(makeTool({
            name: 'jira__read_post',
            mcp_bare_name: 'read_post',
            server_origin: 'https://mcp.atlassian.com',
            arguments: {post_id: 'p1'},
        }));

        expect(mockGetPost).not.toHaveBeenCalled();
        expect(screen.getByText('Post Id')).not.toBeNull();
    });

    test('an unknown embedded tool renders the generic field list', () => {
        renderTool(makeTool({
            name: 'mattermost__archive_channel',
            mcp_bare_name: 'archive_channel',
            server_origin: 'embedded://mattermost',
            arguments: {channel_id: 'chan1', reason: 'obsolete'},
        }));

        expect(screen.getByText('Channel Id')).not.toBeNull();
        expect(screen.getByText('Reason')).not.toBeNull();
        expect(screen.getByText('obsolete')).not.toBeNull();
    });

    test('routes a pending embedded create_post to the post-to-be preview', () => {
        renderTool(makeTool({
            name: 'mattermost__create_post',
            mcp_bare_name: 'create_post',
            server_origin: 'embedded://mattermost',
            arguments: {channel_id: 'chan1', channel_display_name: 'Town Square', message: 'Deploy is done!'},
        }));

        expect(screen.getByText('post-preview-box')).not.toBeNull();

        // The preview is built from the arguments and the requesting user —
        // no fetch is needed for a post that does not exist yet.
        const metadata = (postMessagePreviewMock.mock.calls[0][0] as {metadata: any}).metadata;
        expect(metadata.post.message).toBe('Deploy is done!');
        expect(metadata.post.user_id).toBe('u-self');
        expect(metadata.channel_display_name).toBe('Town Square');
        expect(mockGetPost).not.toHaveBeenCalled();
    });

    test('the create_post preview is not clickable (the post does not exist yet)', () => {
        const {container} = renderTool(makeTool({
            name: 'mattermost__create_post',
            mcp_bare_name: 'create_post',
            server_origin: 'embedded://mattermost',
            arguments: {channel_id: 'chan1', message: 'Deploy is done!'},
        }));

        const previewBox = screen.getByText('post-preview-box');
        const wrap = previewBox.parentElement as HTMLElement;
        expect(getComputedStyle(wrap).pointerEvents).toBe('none');

        // The read_post preview stays interactive: its post is real.
        container.remove();
        mockGetPost.mockResolvedValue({id: 'p1', user_id: 'u1', channel_id: 'chan1', message: 'real post'});
        renderTool(makeTool({
            id: 'tc2',
            name: 'mattermost__read_post',
            mcp_bare_name: 'read_post',
            server_origin: 'embedded://mattermost',
            arguments: {post_id: 'p1'},
        }));
        return waitFor(() => {
            const readWrap = screen.getByText('post-preview-box').parentElement as HTMLElement;
            expect(getComputedStyle(readWrap).pointerEvents).not.toBe('none');
        });
    });

    test('an executed create_post renders generically — no preview', () => {
        renderTool(makeTool({
            name: 'mattermost__create_post',
            mcp_bare_name: 'create_post',
            server_origin: 'embedded://mattermost',
            status: ToolCallStatus.Success,
            arguments: {channel_id: 'chan1', message: 'hi'},
            result: 'Successfully created post',
        }));

        expect(screen.queryByText('post-preview-box')).toBeNull();
        expect(screen.getByText('Channel Id')).not.toBeNull();
    });

    test('create_post without a message falls back to the generic card', () => {
        renderTool(makeTool({
            name: 'mattermost__create_post',
            mcp_bare_name: 'create_post',
            server_origin: 'embedded://mattermost',
            arguments: {channel_id: 'chan1'},
        }));

        expect(screen.queryByText('post-preview-box')).toBeNull();
        expect(screen.getByText('Channel Id')).not.toBeNull();
    });
});

describe('shell features on routed cards', () => {
    test('the post preview card exposes View raw with the exact payload', async () => {
        mockGetPost.mockResolvedValue({id: 'p1', user_id: 'u1', channel_id: 'chan1', message: 'previewed message'});
        const args = {post_id: 'p1', include_thread: true};

        const {container} = renderTool(makeTool({
            name: 'mattermost__read_post',
            mcp_bare_name: 'read_post',
            server_origin: 'embedded://mattermost',
            arguments: args,
        }));

        await waitFor(() => expect(screen.getByText('post-preview-box')).not.toBeNull());
        fireEvent.click(screen.getByText('View raw'));

        const pre = container.querySelector('pre');
        expect(pre?.textContent).toBe(JSON.stringify(args, null, 2));
    });

    test('a JSON-object result renders as a labeled field list', () => {
        render(
            <Provider store={store}>
                <IntlProvider locale='en'>
                    {renderToolCall({
                        ...makeCtx(makeTool({
                            name: 'some_tool',
                            status: ToolCallStatus.Success,
                            arguments: {q: 'x'},
                            result: JSON.stringify({found_count: 3, summary: 'three results'}),
                        })),
                        showResults: true,
                    })}
                </IntlProvider>
            </Provider>,
        );

        expect(screen.getByText('Response')).not.toBeNull();
        expect(screen.getByText('Found Count')).not.toBeNull();
        expect(screen.getByText('3')).not.toBeNull();
        expect(screen.getByText('Summary')).not.toBeNull();
        expect(screen.getByText('three results')).not.toBeNull();
    });

    test('a plain-text result renders as text, not markdown', () => {
        const result = 'Channel: Town Square\n\n![img](http://evil.example/x.png)';
        const {container} = render(
            <Provider store={store}>
                <IntlProvider locale='en'>
                    {renderToolCall({
                        ...makeCtx(makeTool({
                            name: 'some_tool',
                            status: ToolCallStatus.Success,
                            arguments: {q: 'x'},
                            result,
                        })),
                        showResults: true,
                    })}
                </IntlProvider>
            </Provider>,
        );

        expect(screen.getByText(/Town Square/)).not.toBeNull();
        expect(screen.getByText(/!\[img\]/)).not.toBeNull();
        expect(container.querySelector('img')).toBeNull();
    });
});

describe('channel membership tool cards', () => {
    // Both resolution routes are available: the redux store is seeded above and
    // the client mocks answer for the same IDs, plus for the IDs the store
    // deliberately omits. An ID neither knows is rejected/dropped.
    beforeEach(() => {
        mockGetProfilesByIds.mockImplementation((ids: string[]) => Promise.resolve(
            ids.map((id) => directoryProfiles[id]).filter(Boolean),
        ));
        mockGetChannelById.mockImplementation((id: string) => (directoryChannels[id] ?
            Promise.resolve(directoryChannels[id]) :
            Promise.reject(new Error('not found'))));
    });

    afterEach(() => {
        mockGetProfilesByIds.mockImplementation(() => Promise.resolve([]));
        mockGetChannelById.mockReset();
    });

    const membershipCases: Array<{
        label: string;
        bareName: string;
        args: JSONValue;
        status?: ToolCallStatus;
        named: string[];
        notShown?: string[];
    }> = [
        {
            label: 'add_channel_member names the member and channel its IDs point at',
            bareName: 'add_channel_member',
            args: {user_id: memberUserId, channel_id: memberChannelId},
            named: ['dana.holloway', 'Quarterly Planning'],
            notShown: ['omar.reyes'],
        },
        {
            label: 'add_channel_members names every member in the list',
            bareName: 'add_channel_members',
            args: {channel_id: memberChannelId, user_ids: [memberUserId, secondMemberUserId]},
            named: ['dana.holloway', 'omar.reyes', 'Quarterly Planning'],
        },
        {
            label: 'remove_channel_member names the member and channel its IDs point at',
            bareName: 'remove_channel_member',
            args: {channel_id: memberChannelId, user_id: memberUserId},
            named: ['dana.holloway', 'Quarterly Planning'],
            notShown: ['omar.reyes'],
        },
        {
            label: 'a member and channel the store does not hold are named from the fetched records',
            bareName: 'add_channel_members',
            args: {channel_id: fetchOnlyChannelId, user_ids: [fetchOnlyUserId]},
            named: ['lena.whitfield', 'Incident Response'],
        },
        {
            label: 'a member that cannot be named keeps its ID beside the named members',
            bareName: 'add_channel_members',
            args: {channel_id: memberChannelId, user_ids: [memberUserId, unnamedUserId]},
            named: ['dana.holloway', unnamedUserId, 'Quarterly Planning'],
        },
        {
            label: 'arguments beside the IDs do not stand in for the resolved names',
            bareName: 'add_channel_member',
            args: {user_id: memberUserId, channel_id: memberChannelId, username: 'omar.reyes', channel_display_name: 'Town Square'},
            named: ['dana.holloway', 'Quarterly Planning'],
            notShown: ['omar.reyes', 'Town Square'],
        },
        {
            label: 'an accepted call that has not executed still names the member and channel',
            bareName: 'remove_channel_member',
            args: {channel_id: memberChannelId, user_id: secondMemberUserId},
            status: ToolCallStatus.Accepted,
            named: ['omar.reyes', 'Quarterly Planning'],
            notShown: ['dana.holloway'],
        },
    ];

    test.each(membershipCases)('$label', async ({bareName, args, status, named, notShown}) => {
        const {container} = renderTool(makeTool({
            name: `mattermost__${bareName}`,
            mcp_bare_name: bareName,
            server_origin: 'embedded://mattermost',
            status: status ?? ToolCallStatus.Pending,
            arguments: args,
        }));

        // Resolution may be asynchronous, so settle before reading the card.
        await waitFor(() => {
            for (const readable of named) {
                expect(container.textContent).toContain(readable);
            }
        });

        for (const absent of notShown ?? []) {
            expect(container.textContent).not.toContain(absent);
        }
    });

    const genericCardCases: Array<{label: string; tool: Partial<ToolCall>; visible: string[]; notShown: string[]}> = [
        {
            label: 'a call carrying no channel renders the generic field list',
            tool: {
                name: 'mattermost__add_channel_member',
                mcp_bare_name: 'add_channel_member',
                server_origin: 'embedded://mattermost',
                arguments: {user_id: memberUserId},
            },
            visible: ['User Id', memberUserId],
            notShown: ['dana.holloway'],
        },
        {
            label: 'a member list holding a non-string renders the generic field list',
            tool: {
                name: 'mattermost__add_channel_members',
                mcp_bare_name: 'add_channel_members',
                server_origin: 'embedded://mattermost',
                arguments: {channel_id: memberChannelId, user_ids: [memberUserId, 42]},
            },
            visible: ['Channel Id', memberChannelId],
            notShown: ['dana.holloway', 'Quarterly Planning'],
        },
        {
            label: 'an executed call renders the generic field list',
            tool: {
                name: 'mattermost__add_channel_member',
                mcp_bare_name: 'add_channel_member',
                server_origin: 'embedded://mattermost',
                status: ToolCallStatus.Success,
                arguments: {user_id: memberUserId, channel_id: memberChannelId},
                result: 'Successfully added 1 user(s) to channel',
            },
            visible: ['Channel Id', memberChannelId],
            notShown: ['dana.holloway', 'Quarterly Planning'],
        },
        {
            label: 'a channel that cannot be resolved renders the generic field list',
            tool: {
                name: 'mattermost__remove_channel_member',
                mcp_bare_name: 'remove_channel_member',
                server_origin: 'embedded://mattermost',
                arguments: {channel_id: unresolvableChannelId, user_id: memberUserId},
            },
            visible: ['Channel Id', unresolvableChannelId],
            notShown: ['dana.holloway'],
        },
        {
            label: 'a membership-named tool from an external server renders the generic field list',
            tool: {
                name: 'acme__add_channel_members',
                mcp_bare_name: 'add_channel_members',
                server_origin: 'https://mcp.acme.example',
                arguments: {channel_id: memberChannelId, user_ids: [memberUserId]},
            },
            visible: ['Channel Id', memberChannelId],
            notShown: ['dana.holloway', 'Quarterly Planning'],
        },
    ];

    test.each(genericCardCases)('$label', async ({tool, visible, notShown}) => {
        const {container} = renderTool(makeTool(tool));

        await waitFor(() => {
            for (const readable of visible) {
                expect(container.textContent).toContain(readable);
            }
        });

        // Let any in-flight resolution settle before checking that the card
        // named nothing.
        await act(async () => {
            await Promise.resolve();
        });

        for (const absent of notShown) {
            expect(container.textContent).not.toContain(absent);
        }
    });

    test('a pending membership call still renders a card that can be accepted', async () => {
        const onApprove = jest.fn();
        const tool = makeTool({
            name: 'mattermost__add_channel_members',
            mcp_bare_name: 'add_channel_members',
            server_origin: 'embedded://mattermost',
            arguments: {channel_id: memberChannelId, user_ids: [memberUserId]},
        });

        render(
            <Provider store={store}>
                <IntlProvider locale='en'>
                    {renderToolCall({...makeCtx(tool), onApprove, onReject: jest.fn()})}
                </IntlProvider>
            </Provider>,
        );

        await waitFor(() => expect(screen.getByText('Add Channel Members')).not.toBeNull());

        fireEvent.click(screen.getByText('Accept'));
        expect(onApprove).toHaveBeenCalled();
    });
});

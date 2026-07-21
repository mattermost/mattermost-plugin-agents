// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {JSONValue} from '../tool_types';

import {
    parseCreatePost,
    parseDm,
    parseGroupMessage,
    parseSearchPosts,
    parseSearchUsers,
    parseReadPost,
    parseGetChannelInfo,
} from './rich_card_parsers';

// Redacted (non-requester) calls have null arguments; every parser must return
// null so the registry falls back to the generic card.
const REDACTED = null as unknown as JSONValue;

describe('parseCreatePost', () => {
    test('parses a valid create_post payload', () => {
        expect(parseCreatePost({
            channel_id: 'chan1',
            channel_display_name: 'Town Square',
            team_display_name: 'Eng',
            message: 'hello',
            root_id: 'root1',
        })).toEqual({
            message: 'hello',
            channelId: 'chan1',
            channelDisplayName: 'Town Square',
            teamDisplayName: 'Eng',
            rootId: 'root1',
        });
    });

    test('requires a non-empty message', () => {
        expect(parseCreatePost({channel_id: 'c', message: ''})).toBeNull();
        expect(parseCreatePost({channel_id: 'c'})).toBeNull();
    });

    test('omits absent optional fields', () => {
        expect(parseCreatePost({message: 'hi'})).toEqual({message: 'hi'});
    });

    test('returns null for redacted / malformed args', () => {
        expect(parseCreatePost(REDACTED)).toBeNull();
        expect(parseCreatePost('nope' as unknown as JSONValue)).toBeNull();
        expect(parseCreatePost([] as unknown as JSONValue)).toBeNull();
    });
});

describe('parseDm', () => {
    test('parses with and without a username', () => {
        expect(parseDm({username: 'alice', message: 'hi'})).toEqual({username: 'alice', message: 'hi'});
        expect(parseDm({message: 'note to self'})).toEqual({message: 'note to self'});
    });

    test('requires a message', () => {
        expect(parseDm({username: 'alice'})).toBeNull();
        expect(parseDm(REDACTED)).toBeNull();
    });
});

describe('parseGroupMessage', () => {
    test('parses at least one username plus a message', () => {
        expect(parseGroupMessage({usernames: ['a', 'b'], message: 'hey'})).toEqual({usernames: ['a', 'b'], message: 'hey'});
    });

    test('rejects empty or non-string usernames', () => {
        expect(parseGroupMessage({usernames: [], message: 'hey'})).toBeNull();
        expect(parseGroupMessage({usernames: ['a', 2], message: 'hey'})).toBeNull();
        expect(parseGroupMessage({message: 'hey'})).toBeNull();
    });

    test('requires a message', () => {
        expect(parseGroupMessage({usernames: ['a', 'b']})).toBeNull();
        expect(parseGroupMessage(REDACTED)).toBeNull();
    });
});

describe('parseSearchPosts', () => {
    test('extracts the query and only the present filters', () => {
        expect(parseSearchPosts({query: 'roadmap', from: 'sysadmin', after: '2026-01-01'})).toEqual({
            query: 'roadmap',
            filters: [
                {key: 'from', value: 'sysadmin'},
                {key: 'after', value: '2026-01-01'},
            ],
        });
    });

    test('requires a query', () => {
        expect(parseSearchPosts({from: 'x'})).toBeNull();
        expect(parseSearchPosts(REDACTED)).toBeNull();
    });
});

describe('parseSearchUsers', () => {
    test('extracts the term and a numeric limit', () => {
        expect(parseSearchUsers({term: 'john', limit: 5})).toEqual({
            term: 'john',
            filters: [{key: 'limit', value: '5'}],
        });
    });

    test('requires a term', () => {
        expect(parseSearchUsers({limit: 5})).toBeNull();
        expect(parseSearchUsers(REDACTED)).toBeNull();
    });
});

describe('parseReadPost', () => {
    test('parses post_id and include_thread', () => {
        expect(parseReadPost({post_id: 'p1', include_thread: false})).toEqual({postId: 'p1', includeThread: false});
        expect(parseReadPost({post_id: 'p1'})).toEqual({postId: 'p1'});
    });

    test('requires a post_id', () => {
        expect(parseReadPost({include_thread: true})).toBeNull();
        expect(parseReadPost(REDACTED)).toBeNull();
    });
});

describe('parseGetChannelInfo', () => {
    test('parses any single identifying field', () => {
        expect(parseGetChannelInfo({channel_id: 'c1'})).toEqual({channelId: 'c1'});
        expect(parseGetChannelInfo({channel_name: 'town'})).toEqual({channelName: 'town'});
        expect(parseGetChannelInfo({team_id: 't1'})).toEqual({teamId: 't1'});
    });

    test('returns null when no identifier is present', () => {
        expect(parseGetChannelInfo({})).toBeNull();
        expect(parseGetChannelInfo(REDACTED)).toBeNull();
    });
});

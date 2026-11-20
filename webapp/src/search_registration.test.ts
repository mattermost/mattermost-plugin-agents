// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createStore, Store, UnknownAction} from 'redux';
import {GlobalState} from '@mattermost/types/store';

import manifest from '@/manifest';

import {syncSearchRegistration} from './search_registration';

type Availability = {sku: string; searchEnabled: boolean; bots: unknown[]};

const pluginKey = 'plugins-' + manifest.id;

const makeState = ({sku, searchEnabled, bots}: Availability) => ({
    entities: {general: {config: {}, license: {SkuShortName: sku}}},
    [pluginKey]: {searchEnabled, bots},
});

const makeStore = (initial: Availability) => createStore((state: any = makeState(initial), action: any) => {
    if (action.type === 'SET') {
        return makeState(action.availability);
    }
    return state;
}) as unknown as Store<GlobalState, UnknownAction>;

const available: Availability = {sku: 'enterprise', searchEnabled: true, bots: [{id: 'bot'}]};

describe('syncSearchRegistration', () => {
    test.each([
        {name: 'registers when search is available', availability: available, registered: true},
        {name: 'skips registration below Enterprise', availability: {...available, sku: 'professional'}, registered: false},
        {name: 'skips registration when search is disabled', availability: {...available, searchEnabled: false}, registered: false},
        {name: 'skips registration without agents', availability: {...available, bots: []}, registered: false},
    ])('$name', ({availability, registered}) => {
        const store = makeStore(availability);
        const register = jest.fn(() => 'id');

        syncSearchRegistration(store, register, jest.fn());

        expect(register).toHaveBeenCalledTimes(registered ? 1 : 0);
    });

    test('registers once even when registering dispatches to the store', () => {
        const store = makeStore(available);
        const register = jest.fn(() => {
            store.dispatch({type: 'PLUGIN_COMPONENT_REGISTERED'});
            return 'id';
        });

        syncSearchRegistration(store, register, jest.fn());
        store.dispatch({type: 'OTHER'});

        expect(register).toHaveBeenCalledTimes(1);
    });

    test('unregisters when search becomes unavailable and registers again when it returns', () => {
        const store = makeStore(available);
        let nextId = 0;
        const register = jest.fn(() => `id-${++nextId}`);
        const unregister = jest.fn();

        syncSearchRegistration(store, register, unregister);
        store.dispatch({type: 'SET', availability: {...available, searchEnabled: false}});

        expect(unregister).toHaveBeenCalledWith('id-1');

        store.dispatch({type: 'SET', availability: available});

        expect(register).toHaveBeenCalledTimes(2);
        expect(unregister).toHaveBeenCalledTimes(1);
    });
});

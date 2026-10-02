// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// jsdom has no scrollIntoView; compass-ui Select and Combobox call it when highlighting options.
if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = jest.fn();
}

export {};

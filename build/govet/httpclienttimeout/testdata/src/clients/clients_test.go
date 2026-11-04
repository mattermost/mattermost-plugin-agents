// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package clients

import "net/http"

var testClient = &http.Client{Transport: http.DefaultTransport}

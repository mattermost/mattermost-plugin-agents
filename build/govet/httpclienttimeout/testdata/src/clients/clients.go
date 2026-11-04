// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package clients

import (
	"net/http"
	"time"
)

type aliasedClient = http.Client

var configuredTimeout = 5 * time.Second

type notAClient struct {
	Transport http.RoundTripper
}

var (
	missing = http.Client{} // want `http.Client without Timeout`

	withTimeout = http.Client{Timeout: time.Second}

	zeroTimeout = http.Client{Timeout: 0} // want `http.Client without Timeout`

	negativeTimeout = http.Client{Timeout: -time.Second} // want `http.Client without Timeout`

	variableTimeout = http.Client{Timeout: configuredTimeout}

	pointer = &http.Client{Transport: http.DefaultTransport} // want `http.Client without Timeout`

	pointerWithTimeout = &http.Client{Transport: http.DefaultTransport, Timeout: time.Second}

	elided = []*http.Client{
		{Transport: http.DefaultTransport}, // want `http.Client without Timeout`
		{Timeout: time.Second},
	}

	elidedValues = map[string]http.Client{
		"a": {}, // want `http.Client without Timeout`
	}

	aliased = aliasedClient{} // want `http.Client without Timeout`

	multiLine = &http.Client{ // want `http.Client without Timeout`
		Transport: http.DefaultTransport,
	}

	suppressed = &http.Client{Transport: http.DefaultTransport} //nolint:httpclienttimeout // a context deadline bounds every request

	suppressedAmongOthers = http.Client{} //nolint:gosec,httpclienttimeout // a context deadline bounds every request

	otherLinterSuppressed = http.Client{} //nolint:gocritic // want `http.Client without Timeout`

	otherStruct = notAClient{Transport: http.DefaultTransport}
)

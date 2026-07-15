// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

// Logger is the minimal logging surface this package needs; satisfied by
// *pluginapi.LogService. A nil Logger disables logging (tests).
type Logger interface {
	Debug(message string, keyValuePairs ...any)
	Info(message string, keyValuePairs ...any)
	Warn(message string, keyValuePairs ...any)
	Error(message string, keyValuePairs ...any)
}

func logDebug(log Logger, message string, keyValuePairs ...any) {
	if log != nil {
		log.Debug(message, keyValuePairs...)
	}
}

func logInfo(log Logger, message string, keyValuePairs ...any) {
	if log != nil {
		log.Info(message, keyValuePairs...)
	}
}

func logWarn(log Logger, message string, keyValuePairs ...any) {
	if log != nil {
		log.Warn(message, keyValuePairs...)
	}
}

func logError(log Logger, message string, keyValuePairs ...any) {
	if log != nil {
		log.Error(message, keyValuePairs...)
	}
}

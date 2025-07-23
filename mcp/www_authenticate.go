// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"net/url"
	"strings"
)

// parseWWWAuthenticateHeader parses the WWW-Authenticate header to extract resource_metadata URL
// According to RFC 9728, the format is: WWW-Authenticate: Bearer resource_metadata="..."
func parseWWWAuthenticateHeader(header string) (string, error) {
	if header == "" {
		return "", fmt.Errorf("empty WWW-Authenticate header")
	}

	// Input length validation to prevent ReDoS attacks
	const maxHeaderLength = 4096 // 4KB limit
	if len(header) > maxHeaderLength {
		return "", fmt.Errorf("WWW-Authenticate header too long (max %d bytes)", maxHeaderLength)
	}

	// Split on commas to handle multiple challenges
	challenges := strings.Split(header, ",")

	for _, challenge := range challenges {
		challenge = strings.TrimSpace(challenge)
		if challenge == "" {
			continue
		}

		// Look for resource_metadata parameter in this challenge
		metadataURL := extractResourceMetadata(challenge)
		if metadataURL != "" {
			// Validate URL format and length
			if err := validateMetadataURL(metadataURL); err != nil {
				return "", err
			}
			return metadataURL, nil
		}
	}

	return "", fmt.Errorf("resource_metadata not found in WWW-Authenticate header")
}

// extractResourceMetadata safely extracts resource_metadata parameter from a challenge string
func extractResourceMetadata(challenge string) string {
	// Convert to lowercase for case-insensitive matching
	lowerChallenge := strings.ToLower(challenge)

	// Find resource_metadata parameter
	paramIndex := strings.Index(lowerChallenge, "resource_metadata")
	if paramIndex == -1 {
		return ""
	}

	// Find the equals sign after resource_metadata
	searchStart := paramIndex + len("resource_metadata")
	remaining := challenge[searchStart:]

	// Skip whitespace (but limit to prevent ReDoS)
	equalsIndex := -1
	whitespaceCount := 0
	for i, char := range remaining {
		if char == ' ' || char == '\t' {
			whitespaceCount++
			if whitespaceCount > 10 { // Limit excessive whitespace
				return ""
			}
			continue
		}
		if char == '=' {
			equalsIndex = i
			break
		}
		// If we hit a non-whitespace, non-equals character, this isn't our parameter
		return ""
	}

	if equalsIndex == -1 {
		return ""
	}

	// Skip whitespace after equals
	valueStart := searchStart + equalsIndex + 1
	remaining = challenge[valueStart:]
	whitespaceCount = 0
	for i, char := range remaining {
		if char == ' ' || char == '\t' {
			whitespaceCount++
			if whitespaceCount > 10 { // Limit excessive whitespace
				return ""
			}
			continue
		}
		valueStart += i
		break
	}

	if valueStart >= len(challenge) {
		return ""
	}

	// Extract quoted value
	quote := challenge[valueStart]
	if quote != '"' && quote != '\'' {
		return ""
	}

	// Find closing quote
	valueEnd := strings.Index(challenge[valueStart+1:], string(quote))
	if valueEnd == -1 {
		return ""
	}

	return challenge[valueStart+1 : valueStart+1+valueEnd]
}

// validateMetadataURL validates the extracted URL
func validateMetadataURL(metadataURL string) error {
	if metadataURL == "" {
		return fmt.Errorf("empty resource_metadata URL")
	}

	// Length validation
	const maxURLLength = 2048 // Common URL length limit
	if len(metadataURL) > maxURLLength {
		return fmt.Errorf("resource_metadata URL too long (max %d bytes)", maxURLLength)
	}

	// Basic URL format validation
	parsedURL, err := url.Parse(metadataURL)
	if err != nil {
		return fmt.Errorf("invalid resource_metadata URL format: %v", err)
	}

	if parsedURL.Scheme == "" {
		return fmt.Errorf("resource_metadata URL missing scheme")
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("resource_metadata URL missing host")
	}

	return nil
}

// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFetchFileData_FilePathValidation(t *testing.T) {
	// Set up test data directory
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatalf("Failed to create test data directory: %v", err)
	}

	// Override GetDataDirectoryInternal for this test
	originalGetDataDirectory := GetDataDirectoryInternal
	GetDataDirectoryInternal = func() (string, error) {
		return dataDir, nil
	}
	t.Cleanup(func() { GetDataDirectoryInternal = originalGetDataDirectory })

	// Create a temporary test file in data directory
	testFile := "test_file.txt"
	testContent := "test content"
	testFilePath := filepath.Join(dataDir, testFile)
	if err := os.WriteFile(testFilePath, []byte(testContent), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(testFilePath)

	// Create another test file for the complex separator test
	testFile2 := "file.txt"
	testFile2Path := filepath.Join(dataDir, testFile2)
	if err := os.WriteFile(testFile2Path, []byte(testContent), 0600); err != nil {
		t.Fatalf("Failed to create second test file: %v", err)
	}
	defer os.Remove(testFile2Path)

	// Get current working directory for test validation
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	testCases := []struct {
		name       string
		filespec   string
		shouldFail bool
	}{
		{
			name:       "Normal file in current directory",
			filespec:   testFile,
			shouldFail: false,
		},
		{
			name:       "Relative path with parent directory",
			filespec:   "../" + testFile,
			shouldFail: true,
		},
		{
			name:       "Multiple parent directories",
			filespec:   "../../etc/passwd",
			shouldFail: true,
		},
		{
			name:       "System absolute path",
			filespec:   "/etc/passwd",
			shouldFail: true,
		},
		{
			name:       "Local absolute path should be rejected",
			filespec:   cwd + "/" + testFile,
			shouldFail: true,
		},
		{
			name:       "Complex path traversal after cleaning",
			filespec:   "documents/../../../etc/passwd",
			shouldFail: true,
		},
		{
			name:       "Path with multiple separators",
			filespec:   "folder///../file.txt",
			shouldFail: false, // Should resolve to "file.txt" which is valid
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, testErr := fetchFileDataForLocal(tc.filespec, AccessModeLocal)

			if tc.shouldFail {
				if testErr == nil {
					t.Errorf("Expected fetchFileData to fail for %s, but it succeeded", tc.filespec)
				}
			} else {
				if testErr != nil {
					t.Errorf("Expected fetchFileData to succeed for %s, but it failed: %v", tc.filespec, testErr)
				}
			}
		})
	}
}

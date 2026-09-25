package repository

import (
	"context"
	"testing"
)

func TestLocalRepositoryAdapter(t *testing.T) {
	tempDir := t.TempDir()

	repo, err := NewLocalRepository(tempDir)
	if err != nil {
		t.Fatalf("failed to create LocalRepository: %v", err)
	}

	ctx := context.Background()
	testPath := "hello.txt"
	testContent := []byte("hello domain adapter")

	if err := repo.FileSystem().WriteFile(ctx, testPath, testContent, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	readData, err := repo.FileSystem().ReadFile(ctx, testPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(readData) != string(testContent) {
		t.Errorf("got %q, want %q", string(readData), string(testContent))
	}

	stat, err := repo.FileSystem().Stat(ctx, testPath)
	if err != nil || stat.Name != "hello.txt" {
		t.Errorf("Stat failed or unexpected name: %v, %+v", err, stat)
	}

	// Verify escape prevention
	_, err = repo.FileSystem().ReadFile(ctx, "../outside.txt")
	if err == nil {
		t.Error("expected error reading outside repository root, got nil")
	}
}

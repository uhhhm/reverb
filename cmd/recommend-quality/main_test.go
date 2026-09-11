package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestDatabaseRequiresMatchingSourceResponses(t *testing.T) {
	err := run(context.Background(), []string{"-db", "/tmp/reverb-quality-test.db"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "-record-cache") || !strings.Contains(err.Error(), "-fixture") {
		t.Fatalf("error = %v, want instructions to capture or replay matching responses", err)
	}
}

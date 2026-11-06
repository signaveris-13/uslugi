package config

import (
	"reflect"
	"testing"
)

func TestParseIDs(t *testing.T) {
	got, err := parseIDs(" 123, 456 ,,789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []int64{123, 456, 789}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	if _, err := parseIDs("12a"); err == nil {
		t.Fatal("expected error for non-numeric id")
	}

	empty, err := parseIDs("")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty input: got %v, %v", empty, err)
	}
}

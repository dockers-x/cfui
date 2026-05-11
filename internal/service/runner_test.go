package service

import (
	"testing"
	"time"
)

func TestNewRestartBackoffSchedule(t *testing.T) {
	b := newBackoff(5*time.Millisecond, 40*time.Millisecond, 5*time.Millisecond, true)

	want := []time.Duration{
		5 * time.Millisecond,
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		40 * time.Millisecond,
		40 * time.Millisecond,
	}

	for i, expected := range want {
		if got := b.Duration(); got != expected {
			t.Fatalf("duration[%d] = %v, want %v", i, got, expected)
		}
	}
}

func TestBackoffDecayResetsAttempts(t *testing.T) {
	b := newBackoff(5*time.Millisecond, 40*time.Millisecond, 5*time.Millisecond, true)

	if got := b.Duration(); got != 5*time.Millisecond {
		t.Fatalf("first duration = %v, want 5ms", got)
	}
	if got := b.Duration(); got != 10*time.Millisecond {
		t.Fatalf("second duration = %v, want 10ms", got)
	}

	time.Sleep(20 * time.Millisecond)

	if got := b.Duration(); got != 5*time.Millisecond {
		t.Fatalf("duration after decay = %v, want 5ms", got)
	}
}

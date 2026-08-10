package cloudflared

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestDuplicateStartDoesNotOverwriteLiveRunState(t *testing.T) {
	var optionsCalls atomic.Int32
	inst := NewInstance("test", func() (Options, error) {
		optionsCalls.Add(1)
		return Options{}, errors.New("configuration became invalid")
	})
	inst.mu.Lock()
	inst.running = true
	inst.phase = "running"
	inst.ctx, inst.cancel = context.WithCancel(context.Background())
	inst.done = make(chan struct{})
	inst.generation = 1
	inst.mu.Unlock()
	t.Cleanup(func() { inst.cancel() })

	if err := inst.Start(); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("duplicate Start error = %v, want ErrAlreadyRunning", err)
	}
	if got := optionsCalls.Load(); got != 0 {
		t.Fatalf("duplicate Start read options %d time(s)", got)
	}
	status := inst.Status()
	if !status.Running || status.Phase != "running" || status.LastError != nil {
		t.Fatalf("duplicate Start overwrote live state: %#v", status)
	}
}

func TestStopInvalidatesStartStillReadingOptions(t *testing.T) {
	optionsStarted := make(chan struct{})
	releaseOptions := make(chan struct{})
	wantErr := errors.New("invalid options")
	inst := NewInstance("test", func() (Options, error) {
		close(optionsStarted)
		<-releaseOptions
		return Options{}, wantErr
	})
	startDone := make(chan error, 1)
	go func() { startDone <- inst.Start() }()
	<-optionsStarted
	if got := inst.Status().Phase; got != "starting" {
		t.Fatalf("phase while options load = %q, want starting", got)
	}
	if err := inst.Stop(); err != nil {
		t.Fatal(err)
	}
	close(releaseOptions)
	if err := <-startDone; !errors.Is(err, wantErr) {
		t.Fatalf("Start error = %v, want %v", err, wantErr)
	}
	status := inst.Status()
	if status.Running || status.Phase != "stopped" || status.LastError != nil {
		t.Fatalf("stale start overwrote stopped state: %#v", status)
	}
}

func TestInstanceStatusIncludesLifecycleAndRetryMetadata(t *testing.T) {
	inst := NewInstance("test", func() (Options, error) { return Options{Token: "tok", AutoRestart: true}, nil })
	if got := inst.Status().Phase; got != "stopped" {
		t.Fatalf("initial phase = %q", got)
	}
	next := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	inst.mu.Lock()
	inst.running = false
	inst.phase = "reconnecting"
	inst.lastError = errors.New("connection refused")
	inst.restartCount = 3
	inst.nextRetryAt = next
	inst.currentProtocol = "http2"
	inst.mu.Unlock()

	status := inst.Status()
	if status.Phase != "reconnecting" || status.RetryCount != 3 || status.Protocol != "http2" || status.LastError == nil {
		t.Fatalf("unexpected status: %#v", status)
	}
	if status.NextRetryAt == nil || !status.NextRetryAt.Equal(next) {
		t.Fatalf("next retry = %v, want %v", status.NextRetryAt, next)
	}
}

func TestMaybeAutoRestartPublishesWaitingState(t *testing.T) {
	inst := NewInstance("test", func() (Options, error) { return Options{Token: "tok", AutoRestart: true}, nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		inst.maybeAutoRestart(ctx)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		status := inst.Status()
		if status.Phase == "reconnecting" {
			if status.RetryCount != 1 || status.NextRetryAt == nil {
				t.Fatalf("retry metadata missing: %#v", status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconnecting phase was not published: %#v", status)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("restart wait did not honor cancellation")
	}
	if got := inst.Status().Phase; got != "stopped" {
		t.Fatalf("phase after canceled retry = %q", got)
	}
}

func TestStopTimeoutKeepsStuckRunNonStartable(t *testing.T) {
	inst := NewInstance("test", func() (Options, error) {
		return Options{}, errors.New("options must not be read while old run is stuck")
	})
	ctx, cancel := context.WithCancel(context.Background())
	inst.mu.Lock()
	inst.ctx = ctx
	inst.cancel = cancel
	inst.done = make(chan struct{})
	inst.running = true
	inst.phase = "running"
	inst.stopTimeout = time.Millisecond
	inst.generation = 1
	inst.mu.Unlock()

	if err := inst.Stop(); err == nil {
		t.Fatal("Stop returned nil after timeout")
	}
	status := inst.Status()
	if !status.Running || status.Phase != "stopping" || status.LastError == nil {
		t.Fatalf("timeout made stuck run startable: %#v", status)
	}
	if err := inst.Start(); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Start error = %v, want ErrAlreadyRunning", err)
	}
}

func TestStopInvalidatesAutoRestartAfterTimerFires(t *testing.T) {
	var optionsCalls atomic.Int32
	inst := NewInstance("test", func() (Options, error) {
		optionsCalls.Add(1)
		return Options{Token: "tok", AutoRestart: true}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	inst.mu.Lock()
	inst.ctx = ctx
	inst.cancel = cancel
	inst.running = false
	inst.phase = "reconnecting"
	inst.generation = 1
	inst.mu.Unlock()

	if err := inst.Stop(); err != nil {
		t.Fatal(err)
	}
	expectedGeneration := uint64(1)
	if err := inst.start(&expectedGeneration, ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("stale restart error = %v, want context.Canceled", err)
	}
	if got := optionsCalls.Load(); got != 0 {
		t.Fatalf("stale restart read options %d time(s)", got)
	}
}

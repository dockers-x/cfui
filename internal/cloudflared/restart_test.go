package cloudflared

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestRecoveryContinuesAfterFastAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		configurationRemoved := errors.New("profile removed before recovery")
		inst := NewInstance("outage", func() (Options, error) {
			if calls.Add(1) > 1 {
				return Options{}, configurationRemoved
			}
			return Options{Token: "test", AutoRestart: true}, nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		inst.ctx, inst.cancel = ctx, cancel
		inst.generation = 1
		inst.restartCount = maxFastRestartAttempts
		inst.lastRestart = time.Now().Add(-time.Hour)
		done := make(chan struct{})
		go func() {
			defer close(done)
			inst.maybeAutoRestartForRun(ctx, 1)
		}()
		synctest.Wait()
		status := inst.Status()
		if status.Phase != "reconnecting" || status.NextRetryAt == nil || status.RetryCount != maxFastRestartAttempts+1 {
			t.Fatalf("long outage lost recovery scheduling: %#v", status)
		}
		delay := time.Until(*status.NextRetryAt)
		if delay < restartSlowDelay || delay >= restartSlowDelay+restartSlowJitter {
			t.Fatalf("slow recovery delay = %s", delay)
		}
		time.Sleep(delay)
		synctest.Wait()
		<-done
		if got := calls.Load(); got != 2 {
			t.Fatalf("recovery did not re-read launch options: calls = %d", got)
		}
		if !errors.Is(inst.Status().LastError, configurationRemoved) {
			t.Fatalf("recovery did not execute the next launch: %#v", inst.Status())
		}
	})
}

func TestStopCancelsSlowRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		inst := NewInstance("outage", func() (Options, error) {
			calls.Add(1)
			return Options{Token: "test", AutoRestart: true}, nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		inst.ctx, inst.cancel = ctx, cancel
		inst.generation = 1
		inst.restartCount = maxFastRestartAttempts
		done := make(chan struct{})
		go func() {
			defer close(done)
			inst.maybeAutoRestartForRun(ctx, 1)
		}()
		synctest.Wait()
		if inst.Status().NextRetryAt == nil {
			t.Fatal("slow recovery was not scheduled")
		}
		if err := inst.Stop(); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		<-done
		time.Sleep(restartSlowDelay + restartSlowJitter)
		if status := inst.Status(); status.Phase != "stopped" || status.NextRetryAt != nil {
			t.Fatalf("stopped instance retained recovery state: %#v", status)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("manual stop allowed another launch: calls = %d", got)
		}
	})
}

func TestRestartDelayBounds(t *testing.T) {
	inst := NewInstance("jitter", nil)
	inst.mu.Lock()
	defer inst.mu.Unlock()
	for attempt := range 100 {
		inst.restartCount = attempt
		delay := inst.nextRestartDelay()
		if attempt < maxFastRestartAttempts {
			if delay < 0 || delay > restartBackoffMaxDelay {
				t.Fatalf("fast delay for attempt %d = %s", attempt, delay)
			}
		} else if delay < restartSlowDelay || delay >= restartSlowDelay+restartSlowJitter {
			t.Fatalf("slow delay for attempt %d = %s", attempt, delay)
		}
	}
}

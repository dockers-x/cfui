package logger

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestLogBroadcasterUnsubscribeAfterCloseDoesNotPanic(t *testing.T) {
	b := NewLogBroadcaster(10)
	ch := b.Subscribe("test")

	b.Close()
	b.Unsubscribe(ch)
	b.Close()
	if _, open := <-b.Subscribe("late"); open {
		t.Fatal("a subscriber registered after shutdown")
	}
}

func TestInactiveSubscriberCleanupCanBroadcastWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := NewLogBroadcaster(10)
		previous := Sugar
		Sugar = zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
			zapcore.AddSync(newBroadcastWriter(b)), zap.WarnLevel)).Sugar()
		defer func() { Sugar = previous }()
		defer b.Close()
		ch := b.Subscribe("idle-client")
		time.Sleep(subscriberTimeout + 2*cleanupInterval)
		synctest.Wait()
		if _, open := <-ch; open {
			t.Fatal("inactive subscriber is still open")
		}
		if logs := b.GetRecentLogs(); len(logs) != 1 || !strings.Contains(logs[0], "idle-client") {
			t.Fatalf("unexpected cleanup log: %v", logs)
		}
	})
}

package fanout

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type testMetrics struct{}

func (testMetrics) SetSyncStatus(bool, string) {}
func testFanout(server *httptest.Server) *fanout {
	return &fanout{cfg: settings{Peers: []string{server.URL}}, client: server.Client(), metricsServer: testMetrics{}, pending: make(map[string]chan struct{})}
}
func pendingCount(f *fanout) int {
	f.pendingMu.Lock()
	defer f.pendingMu.Unlock()
	return len(f.pending)
}

func TestFailedNotificationRetriesWithoutAnotherEvent(t *testing.T) {
	var online atomic.Bool
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if !online.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	f := testFanout(server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.fireForPeerEvent(ctx, "peer:deleted:sync", "revoked")
	require.Eventually(t, func() bool { return attempts.Load() > 0 }, time.Second, time.Millisecond)
	online.Store(true)
	require.Eventually(t, func() bool { return pendingCount(f) == 0 }, 5*time.Second, 10*time.Millisecond)
	completed := attempts.Load()
	require.GreaterOrEqual(t, completed, int32(2))
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, completed, attempts.Load())
}

func TestNotificationsForSamePeerAreCoalesced(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			close(started)
			<-release
		}
		require.Equal(t, "peer", r.URL.Query().Get("peer_id"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	f := testFanout(server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.fireForPeerEvent(ctx, "peer:created:sync", "peer")
	<-started
	for i := 0; i < 10; i++ {
		f.fireForPeerEvent(ctx, "peer:updated:sync", "peer")
	}
	require.Equal(t, 1, pendingCount(f))
	close(release)
	require.Eventually(t, func() bool { return pendingCount(f) == 0 }, time.Second, time.Millisecond)
	require.Equal(t, int32(2), attempts.Load())
}

func TestPendingRetryStopsWithApplication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	f := testFanout(server)
	ctx, cancel := context.WithCancel(context.Background())
	f.fireForPeerEvent(ctx, "peer:updated:sync", "peer")
	require.Equal(t, 1, pendingCount(f))
	cancel()
	require.Eventually(t, func() bool { return pendingCount(f) == 0 }, time.Second, time.Millisecond)
}

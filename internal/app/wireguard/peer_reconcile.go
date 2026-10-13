package wireguard

import (
	"context"
	"log/slog"
	"time"

	"github.com/fedor-git/wg-portal-2/internal/config"
)

// StartPeerReconciliation periodically repairs missed fanout events using the
// existing peer diff. Start it after interface initialization.
func (m Manager) StartPeerReconciliation(ctx context.Context) {
	interval := m.cfg.Core.PeerReconcileInterval
	if !m.cfg.Core.Fanout.Enabled || interval <= 0 {
		return
	}
	go func() {
		m.reconcilePeers(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.reconcilePeers(ctx)
			}
		}
	}()
}

func (m Manager) reconcilePeers(ctx context.Context) {
	m.peerSyncMu.Lock()
	defer m.peerSyncMu.Unlock()
	interfaces, err := m.db.GetAllInterfaces(ctx)
	if err != nil {
		slog.Error("failed to list interfaces for peer reconciliation", "error", err)
		return
	}
	for _, iface := range interfaces {
		// An empty backend identifies a local interface in the v1 API.
		if iface.Backend == "" || iface.Backend == config.LocalBackendName {
			peers, err := m.db.GetInterfacePeers(ctx, iface.Identifier)
			if err == nil {
				_, err = m.syncInterfacePeers(ctx, iface, peers)
			}
			if err != nil {
				slog.Error("failed to reconcile peers", "interface", iface.Identifier, "error", err)
			}
		}
	}
}

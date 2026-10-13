package wireguard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/fedor-git/wg-portal-2/internal/app"
	"github.com/fedor-git/wg-portal-2/internal/domain"
)

type peerLister interface {
	GetAllPeers(ctx context.Context) ([]domain.Peer, error)
}

func (m Manager) SyncAllPeersFromDB(ctx context.Context) (int, error) {
	if err := domain.ValidateAdminAccessRights(ctx); err != nil {
		return 0, err
	}
	m.peerSyncMu.Lock()
	defer m.peerSyncMu.Unlock()
	interfaces, err := m.db.GetAllInterfaces(ctx)
	if err != nil {
		return 0, err
	}
	loaded := 0
	for _, iface := range interfaces {
		peers, err := m.db.GetInterfacePeers(ctx, iface.Identifier)
		if err != nil {
			return loaded, err
		}
		count, err := m.syncInterfacePeers(ctx, iface, peers)
		if err != nil {
			return loaded, err
		}
		loaded += count
	}
	return loaded, nil
}

func (m Manager) replacePeers(ctx context.Context, iface domain.InterfaceIdentifier, peers []domain.Peer) error {
	slog.Debug("replacePeers: will write peers to wg0.conf", "iface", iface, "peers_count", len(peers), "peers", peers)

	if err := m.clearPeers(ctx, iface); err != nil {
		return err
	}

	for i := range peers {
		if err := m.savePeers(ctx, &peers[i]); err != nil {
			return fmt.Errorf("add peer %s on %s: %w", peers[i].Identifier, iface, err)
		}
	}

	if m.bus != nil {
		m.bus.Publish(app.TopicPeerInterfaceUpdated, iface)
	}

	return nil
}

func (m Manager) clearPeers(ctx context.Context, iface domain.InterfaceIdentifier) error {
	slog.Debug("clearPeers: clearing all peers from wg0.conf", "iface", iface)
	return m.wg.ClearPeers(ctx, string(iface))
}

func (m Manager) applyPeers(ctx context.Context, peers []domain.Peer) error {
	if len(peers) == 0 {
		return nil
	}

	// Filter out disabled peers first
	enabledPeers := make([]*domain.Peer, 0, len(peers))
	for i := range peers {
		if !peers[i].IsDisabled() {
			enabledPeers = append(enabledPeers, &peers[i])
		}
	}

	if len(enabledPeers) == 0 {
		return nil
	}

	// Apply ALL peers in a single batch call instead of one-by-one
	// This significantly reduces syscalls during startup
	slog.Debug("applying peers batch", "count", len(enabledPeers))
	if err := m.savePeers(ctx, enabledPeers...); err != nil {
		return fmt.Errorf("apply peers batch failed (count %d): %w", len(enabledPeers), err)
	}

	if !app.NoFanout(ctx) && m.bus != nil {
		// Publish single event for entire batch instead of one per peer
		m.bus.Publish(app.TopicPeerUpdated, struct{}{})
	}

	return nil
}

func isNoSuchFile(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "file does not exist")
}

// Applying peers is node-local; it must not be skipped by a cluster-wide DB lock.
func (m Manager) SyncAllPeersFromDBWithLock(ctx context.Context, _ string) (int, error) {
	return m.SyncAllPeersFromDB(ctx)
}

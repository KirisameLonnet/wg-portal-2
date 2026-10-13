package wireguard

import (
	"context"
	"errors"
	"slices"

	"github.com/fedor-git/wg-portal-2/internal/domain"
)

// SyncPeer applies the current DB state before acknowledging a notification.
func (m Manager) SyncPeer(ctx context.Context, id domain.PeerIdentifier) error {
	m.peerSyncMu.Lock()
	defer m.peerSyncMu.Unlock()
	peer, err := m.db.GetPeer(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		interfaces, err := m.db.GetAllInterfaces(ctx)
		if err != nil {
			return err
		}
		for _, iface := range interfaces {
			controller := m.wg.GetController(iface)
			peers, err := controller.GetPeers(ctx, iface.Identifier)
			if err != nil {
				return err
			}
			for _, existing := range peers {
				if existing.Identifier == id {
					if err := controller.DeletePeer(ctx, iface.Identifier, id); err != nil {
						return err
					}
				}
			}
		}
		if m.statsCollector != nil && m.statsCollector.ms != nil {
			m.statsCollector.ms.RemovePeerMetricsByID(string(id))
		}
		return nil
	}
	if err != nil {
		return err
	}
	iface, err := m.db.GetInterface(ctx, peer.InterfaceIdentifier)
	if err != nil {
		return err
	}
	controller := m.wg.GetController(*iface)
	peers, err := controller.GetPeers(ctx, iface.Identifier)
	if err != nil {
		return err
	}
	for _, existing := range peers {
		if existing.Identifier == id {
			if peer.IsDisabled() {
				return controller.DeletePeer(ctx, iface.Identifier, id)
			}
			return m.applyPeerConfiguration(ctx, controller, *peer, &existing)
		}
	}
	if peer.IsDisabled() {
		return nil
	}
	return m.applyPeerConfiguration(ctx, controller, *peer, nil)
}

func (m Manager) syncInterfacePeers(ctx context.Context, iface domain.Interface, peers []domain.Peer) (int, error) {
	controller := m.wg.GetController(iface)
	existing, err := controller.GetPeers(ctx, iface.Identifier)
	if err != nil {
		return 0, err
	}
	desired := make(map[domain.PeerIdentifier]domain.Peer)
	for _, peer := range peers {
		if !peer.IsDisabled() {
			desired[peer.Identifier] = peer
		}
	}
	current := make(map[domain.PeerIdentifier]domain.PhysicalPeer)
	for _, peer := range existing {
		current[peer.Identifier] = peer
		if _, keep := desired[peer.Identifier]; !keep {
			if err := controller.DeletePeer(ctx, iface.Identifier, peer.Identifier); err != nil {
				return 0, err
			}
		}
	}
	for id, peer := range desired {
		if existing, found := current[id]; found {
			err = m.applyPeerConfiguration(ctx, controller, peer, &existing)
		} else {
			err = m.applyPeerConfiguration(ctx, controller, peer, nil)
		}
		if err != nil {
			return 0, err
		}
	}
	return len(desired), nil
}

func (m Manager) applyPeerConfiguration(ctx context.Context, controller InterfaceController, peer domain.Peer, existing *domain.PhysicalPeer) error {
	var desired domain.PhysicalPeer
	if existing != nil {
		desired = *existing
	}
	domain.MergeToPhysicalPeer(&desired, &peer, m.cfg.Core.ForceClientIPAsAllowedIP)
	// The diff describes Linux WireGuard configuration; other backends retain
	// their existing write path, including backend-specific metadata.
	if existing != nil && existing.ImportSource == domain.ControllerTypeLocal && samePeerConfiguration(*existing, desired) {
		return nil
	}
	return controller.SavePeer(ctx, peer.InterfaceIdentifier, peer.Identifier, func(pp *domain.PhysicalPeer) (*domain.PhysicalPeer, error) {
		domain.MergeToPhysicalPeer(pp, &peer, m.cfg.Core.ForceClientIPAsAllowedIP)
		return pp, nil
	})
}

// Runtime handshake/counters and an unconfigured roaming endpoint are not config.
func samePeerConfiguration(current, desired domain.PhysicalPeer) bool {
	if current.PublicKey != desired.PublicKey || current.PresharedKey != desired.PresharedKey || current.PersistentKeepalive != desired.PersistentKeepalive {
		return false
	}
	if endpoint := desired.GetEndpointAddress(); endpoint != nil && current.Endpoint != endpoint.String() {
		return false
	}
	prefixes := func(cidrs []domain.Cidr) []string {
		out := make([]string, len(cidrs))
		for i, cidr := range cidrs {
			out[i] = cidr.Prefix().Masked().String()
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return slices.Equal(prefixes(current.AllowedIPs), prefixes(desired.AllowedIPs))
}

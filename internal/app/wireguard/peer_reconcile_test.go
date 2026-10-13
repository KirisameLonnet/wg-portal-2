package wireguard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fedor-git/wg-portal-2/internal/config"
	"github.com/fedor-git/wg-portal-2/internal/domain"
	"github.com/stretchr/testify/require"
)

type reconcileRepo struct {
	InterfaceAndPeerDatabaseRepo
	peers []domain.Peer
	err   error
}

func (r *reconcileRepo) GetAllInterfaces(context.Context) ([]domain.Interface, error) {
	return []domain.Interface{{Identifier: "wg0"}}, nil // v1 API's default local backend
}
func (r *reconcileRepo) GetInterfacePeers(context.Context, domain.InterfaceIdentifier) ([]domain.Peer, error) {
	return r.peers, r.err
}

type reconcileController struct {
	InterfaceController
	peers          map[domain.PeerIdentifier]domain.PhysicalPeer
	saved, deleted []domain.PeerIdentifier
	readErr        error
}

func (c *reconcileController) GetPeers(context.Context, domain.InterfaceIdentifier) ([]domain.PhysicalPeer, error) {
	var peers []domain.PhysicalPeer
	for _, peer := range c.peers {
		peers = append(peers, peer)
	}
	return peers, c.readErr
}
func (c *reconcileController) DeletePeer(_ context.Context, _ domain.InterfaceIdentifier, id domain.PeerIdentifier) error {
	c.deleted = append(c.deleted, id)
	delete(c.peers, id)
	return nil
}
func (c *reconcileController) SavePeer(_ context.Context, _ domain.InterfaceIdentifier, id domain.PeerIdentifier, update func(*domain.PhysicalPeer) (*domain.PhysicalPeer, error)) error {
	current := c.peers[id]
	endpoint := current.Endpoint
	peer, err := update(&current)
	if err != nil {
		return err
	}
	// A nil WireGuard endpoint update retains the learned endpoint.
	if peer.Endpoint == "" {
		peer.Endpoint = endpoint
	}
	c.saved = append(c.saved, id)
	c.peers[id] = *peer
	return nil
}

func reconcileFixture(t *testing.T) (Manager, *reconcileRepo, *reconcileController, domain.Peer) {
	t.Helper()
	ips, err := domain.CidrsFromString("10.77.0.2/32")
	require.NoError(t, err)
	peer := domain.Peer{
		Identifier: "client", InterfaceIdentifier: "wg0",
		Interface: domain.PeerInterfaceConfig{KeyPair: domain.KeyPair{PublicKey: "client"}, Type: domain.InterfaceTypeClient, Addresses: ips},
	}
	cfg := &config.Config{}
	cfg.Core.ForceClientIPAsAllowedIP = true
	repo := &reconcileRepo{peers: []domain.Peer{peer}}
	controller := &reconcileController{peers: map[domain.PeerIdentifier]domain.PhysicalPeer{}}
	controllers := &ControllerManager{controllers: map[domain.InterfaceBackend]backendInstance{
		config.LocalBackendName: {Implementation: controller},
	}}
	return Manager{cfg: cfg, db: repo, wg: controllers}, repo, controller, peer
}

func TestReconcileRepairsMissedCreationAndDeletion(t *testing.T) {
	m, _, controller, peer := reconcileFixture(t)
	controller.peers["revoked"] = domain.PhysicalPeer{Identifier: "revoked"}
	m.reconcilePeers(context.Background())
	require.Equal(t, []domain.PeerIdentifier{peer.Identifier}, controller.saved)
	require.Equal(t, []domain.PeerIdentifier{"revoked"}, controller.deleted)
	require.NotContains(t, controller.peers, domain.PeerIdentifier("revoked"))
}

func TestReconcilePreservesRoamingAndHandshake(t *testing.T) {
	m, _, controller, peer := reconcileFixture(t)
	var current domain.PhysicalPeer
	domain.MergeToPhysicalPeer(&current, &peer, true)
	current.Endpoint = "203.0.113.4:49000"
	current.LastHandshake = time.Now()
	controller.peers[peer.Identifier] = current
	m.reconcilePeers(context.Background())
	require.Equal(t, current, controller.peers[peer.Identifier])
}

func TestReconcileRepairsAuthorizationWithoutResettingEndpoint(t *testing.T) {
	m, repo, controller, peer := reconcileFixture(t)
	var current domain.PhysicalPeer
	domain.MergeToPhysicalPeer(&current, &peer, true)
	current.Endpoint = "203.0.113.4:49000"
	current.PresharedKey = "old-psk"
	current.PersistentKeepalive = 25
	controller.peers[peer.Identifier] = current
	repo.peers[0].Interface.Addresses, _ = domain.CidrsFromString("10.77.0.9/32")
	m.reconcilePeers(context.Background())
	updated := controller.peers[peer.Identifier]
	require.Equal(t, current.Endpoint, updated.Endpoint)
	require.Empty(t, updated.PresharedKey)
	require.Zero(t, updated.PersistentKeepalive)
	require.Equal(t, "10.77.0.9/32", updated.AllowedIPs[0].String())
}

func TestReconcileRemovesDisabledPeer(t *testing.T) {
	m, repo, controller, peer := reconcileFixture(t)
	controller.peers[peer.Identifier] = domain.PhysicalPeer{Identifier: peer.Identifier}
	now := time.Now()
	repo.peers[0].Disabled = &now
	m.reconcilePeers(context.Background())
	require.Empty(t, controller.peers)
}

func TestReconcileQueryFailuresNeverRemovePeers(t *testing.T) {
	for _, failure := range []string{"database", "kernel"} {
		t.Run(failure, func(t *testing.T) {
			m, repo, controller, peer := reconcileFixture(t)
			controller.peers[peer.Identifier] = domain.PhysicalPeer{Identifier: peer.Identifier}
			repo.peers = nil
			if failure == "database" {
				repo.err = errors.New("database unavailable")
			} else {
				controller.readErr = errors.New("netlink unavailable")
			}
			m.reconcilePeers(context.Background())
			require.Empty(t, controller.saved)
			require.Empty(t, controller.deleted)
			require.Contains(t, controller.peers, peer.Identifier)
		})
	}
}

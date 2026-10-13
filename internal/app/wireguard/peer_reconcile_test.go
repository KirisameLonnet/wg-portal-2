package wireguard

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/fedor-git/wg-portal-2/internal/config"
	"github.com/fedor-git/wg-portal-2/internal/domain"
	"github.com/stretchr/testify/require"
)

type syncRepo struct {
	InterfaceAndPeerDatabaseRepo
	peers map[domain.PeerIdentifier]domain.Peer
	err   error
}

func (r *syncRepo) GetAllInterfaces(context.Context) ([]domain.Interface, error) {
	return []domain.Interface{{Identifier: "wg0"}}, r.err
}
func (r *syncRepo) GetInterface(context.Context, domain.InterfaceIdentifier) (*domain.Interface, error) {
	return &domain.Interface{Identifier: "wg0"}, r.err
}
func (r *syncRepo) GetPeer(_ context.Context, id domain.PeerIdentifier) (*domain.Peer, error) {
	if r.err != nil {
		return nil, r.err
	}
	if peer, ok := r.peers[id]; ok {
		return &peer, nil
	}
	return nil, domain.ErrNotFound
}
func (r *syncRepo) GetInterfacePeers(context.Context, domain.InterfaceIdentifier) ([]domain.Peer, error) {
	peers := make([]domain.Peer, 0, len(r.peers))
	for _, peer := range r.peers {
		peers = append(peers, peer)
	}
	return peers, r.err
}

type syncController struct {
	InterfaceController
	peers          map[domain.PeerIdentifier]domain.PhysicalPeer
	saves, deletes int
	err            error
}

func (c *syncController) GetPeers(context.Context, domain.InterfaceIdentifier) ([]domain.PhysicalPeer, error) {
	peers := make([]domain.PhysicalPeer, 0, len(c.peers))
	for _, peer := range c.peers {
		peers = append(peers, peer)
	}
	return peers, nil
}
func (c *syncController) DeletePeer(_ context.Context, _ domain.InterfaceIdentifier, id domain.PeerIdentifier) error {
	if c.err != nil {
		return c.err
	}
	c.deletes++
	delete(c.peers, id)
	return nil
}
func (c *syncController) SavePeer(_ context.Context, _ domain.InterfaceIdentifier, id domain.PeerIdentifier, update func(*domain.PhysicalPeer) (*domain.PhysicalPeer, error)) error {
	if c.err != nil {
		return c.err
	}
	c.saves++
	peer := c.peers[id]
	peer.ImportSource = domain.ControllerTypeLocal
	endpoint := peer.Endpoint
	next, err := update(&peer)
	if err != nil {
		return err
	}
	if next.Endpoint == "" {
		next.Endpoint = endpoint
	}
	c.peers[id] = *next
	return nil
}
func syncFixture() (Manager, *syncRepo, *syncController) {
	cfg := &config.Config{}
	cfg.Core.ForceClientIPAsAllowedIP = true
	peer := domain.Peer{Identifier: "active", InterfaceIdentifier: "wg0", Interface: domain.PeerInterfaceConfig{KeyPair: domain.KeyPair{PublicKey: "active"}, Addresses: []domain.Cidr{domain.CidrFromPrefix(netip.MustParsePrefix("10.77.0.2/24"))}}}
	var physical domain.PhysicalPeer
	domain.MergeToPhysicalPeer(&physical, &peer, true)
	physical.ImportSource = domain.ControllerTypeLocal
	physical.Endpoint = "192.0.2.1:1234"
	physical.LastHandshake = time.Now()
	physical.BytesUpload = 1234
	repo := &syncRepo{peers: map[domain.PeerIdentifier]domain.Peer{"active": peer}}
	controller := &syncController{peers: map[domain.PeerIdentifier]domain.PhysicalPeer{"active": physical, "revoked": {Identifier: "revoked"}}}
	manager := Manager{cfg: cfg, db: repo, peerSyncMu: &sync.Mutex{}, wg: &ControllerManager{controllers: map[domain.InterfaceBackend]backendInstance{config.LocalBackendName: {Implementation: controller}}}}
	return manager, repo, controller
}

func TestReconciliationOnlyWritesConfigurationDifferences(t *testing.T) {
	m, repo, controller := syncFixture()
	unchanged := controller.peers["active"]
	peer := repo.peers["active"]
	peer.Identifier = "new"
	peer.Interface.PublicKey = "new"
	repo.peers["new"] = peer
	m.reconcilePeers(context.Background())
	require.Equal(t, 1, controller.saves)
	require.Equal(t, 1, controller.deletes)
	require.Equal(t, unchanged, controller.peers["active"])
	m.reconcilePeers(context.Background())
	require.Equal(t, 1, controller.saves)
	require.Equal(t, 1, controller.deletes)
	peer = repo.peers["active"]
	peer.PersistentKeepalive = domain.NewConfigOption(25, true)
	repo.peers["active"] = peer
	require.NoError(t, m.SyncPeer(context.Background(), "active"))
	require.Equal(t, 2, controller.saves)
	require.Equal(t, unchanged.Endpoint, controller.peers["active"].Endpoint)
}

func TestSyncPeerReadsLatestStateAndReportsFailure(t *testing.T) {
	m, repo, controller := syncFixture()
	repo.err = errors.New("DB unavailable")
	require.Error(t, m.SyncPeer(context.Background(), "active"))
	m.reconcilePeers(context.Background())
	require.Zero(t, controller.saves)
	require.Zero(t, controller.deletes)
	repo.err = nil
	controller.err = errors.New("kernel rejected update")
	require.Error(t, m.SyncPeer(context.Background(), "revoked"))
	require.Contains(t, controller.peers, domain.PeerIdentifier("revoked"))
	controller.err = nil
	require.NoError(t, m.SyncPeer(context.Background(), "revoked"))
	require.NotContains(t, controller.peers, domain.PeerIdentifier("revoked"))
	peer := repo.peers["active"]
	now := time.Now()
	peer.Disabled = &now
	repo.peers["active"] = peer
	require.NoError(t, m.SyncPeer(context.Background(), "active"))
	require.NotContains(t, controller.peers, domain.PeerIdentifier("active"))
}

func TestConfigurationComparisonIgnoresRuntimeAndPrefixOrder(t *testing.T) {
	first := domain.CidrFromPrefix(netip.MustParsePrefix("10.77.0.2/32"))
	second := domain.CidrFromPrefix(netip.MustParsePrefix("192.0.2.0/24"))
	current := domain.PhysicalPeer{KeyPair: domain.KeyPair{PublicKey: "peer"}, AllowedIPs: []domain.Cidr{first, second}, PresharedKey: "old", PersistentKeepalive: 25, Endpoint: "192.0.2.1:1234", LastHandshake: time.Now(), BytesUpload: 42}
	desired := current
	desired.AllowedIPs = []domain.Cidr{second, first, first}
	desired.Endpoint = ""
	desired.LastHandshake = time.Time{}
	desired.BytesUpload = 0
	require.True(t, samePeerConfiguration(current, desired))
	desired.PresharedKey = ""
	require.False(t, samePeerConfiguration(current, desired))
	desired.PresharedKey = current.PresharedKey
	desired.PersistentKeepalive = 0
	require.False(t, samePeerConfiguration(current, desired))
	desired.PersistentKeepalive = current.PersistentKeepalive
	desired.AllowedIPs = []domain.Cidr{first}
	require.False(t, samePeerConfiguration(current, desired))
}

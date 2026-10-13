package wireguard

import (
	"context"
	"testing"
	"time"

	"github.com/fedor-git/wg-portal-2/internal/config"
	"github.com/fedor-git/wg-portal-2/internal/domain"
	"github.com/stretchr/testify/require"
)

type reconcileRepo struct{ InterfaceAndPeerDatabaseRepo }

func (reconcileRepo) GetAllInterfaces(context.Context) ([]domain.Interface, error) {
	return []domain.Interface{{Identifier: "wg0"}}, nil
}
func (reconcileRepo) GetInterfacePeers(context.Context, domain.InterfaceIdentifier) ([]domain.Peer, error) {
	return []domain.Peer{{Identifier: "new", InterfaceIdentifier: "wg0", Interface: domain.PeerInterfaceConfig{KeyPair: domain.KeyPair{PublicKey: "new"}}}}, nil
}

type reconcileController struct {
	InterfaceController
	added, removed chan domain.PeerIdentifier
}

func (c reconcileController) GetPeers(context.Context, domain.InterfaceIdentifier) ([]domain.PhysicalPeer, error) {
	return []domain.PhysicalPeer{{Identifier: "revoked"}}, nil
}
func (c reconcileController) DeletePeer(_ context.Context, _ domain.InterfaceIdentifier, id domain.PeerIdentifier) error {
	c.removed <- id
	return nil
}
func (c reconcileController) SavePeer(_ context.Context, _ domain.InterfaceIdentifier, id domain.PeerIdentifier, _ func(*domain.PhysicalPeer) (*domain.PhysicalPeer, error)) error {
	c.added <- id
	return nil
}

func TestPeriodicReconciliationRepairsMissedEvents(t *testing.T) {
	cfg := &config.Config{}
	cfg.Core.Fanout.Enabled = true
	cfg.Core.PeerReconcileInterval = 50 * time.Millisecond
	controller := reconcileController{added: make(chan domain.PeerIdentifier, 1), removed: make(chan domain.PeerIdentifier, 1)}
	m := Manager{cfg: cfg, db: reconcileRepo{}, wg: &ControllerManager{controllers: map[domain.InterfaceBackend]backendInstance{config.LocalBackendName: {Implementation: controller}}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.StartPeerReconciliation(ctx)
	select {
	case id := <-controller.removed:
		require.Equal(t, domain.PeerIdentifier("revoked"), id)
	case <-time.After(time.Second):
		t.Fatal("missed deletion was not recovered")
	}
	select {
	case id := <-controller.added:
		require.Equal(t, domain.PeerIdentifier("new"), id)
	case <-time.After(time.Second):
		t.Fatal("missed creation was not recovered")
	}
}

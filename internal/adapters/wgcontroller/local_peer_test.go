package wgcontroller

import (
	"testing"

	"github.com/fedor-git/wg-portal-2/internal/domain"
	"github.com/stretchr/testify/require"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type peerConfigRecorder struct {
	WgCtrlRepo
	configs []wgtypes.Config
}

func (r *peerConfigRecorder) ConfigureDevice(_ string, cfg wgtypes.Config) error {
	r.configs = append(r.configs, cfg)
	return nil
}

func TestLocalPeerUpdateClearsPSKAndKeepalive(t *testing.T) {
	key, err := wgtypes.GeneratePrivateKey()
	require.NoError(t, err)
	recorder := &peerConfigRecorder{}
	controller := LocalController{wg: recorder}
	peer := domain.PhysicalPeer{Identifier: domain.PeerIdentifier(key.PublicKey().String()), KeyPair: domain.KeyPair{PublicKey: key.PublicKey().String()}}
	require.NoError(t, controller.updatePeer("wg0", &peer))
	require.Len(t, recorder.configs, 1)
	require.False(t, recorder.configs[0].ReplacePeers)
	cfg := recorder.configs[0].Peers[0]
	require.True(t, cfg.UpdateOnly)
	require.NotNil(t, cfg.PresharedKey)
	require.Equal(t, wgtypes.Key{}, *cfg.PresharedKey)
	require.NotNil(t, cfg.PersistentKeepaliveInterval)
	require.Zero(t, *cfg.PersistentKeepaliveInterval)
}

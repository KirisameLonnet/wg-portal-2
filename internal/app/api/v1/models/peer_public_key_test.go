package models

import (
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/require"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func TestPeerAcceptsExistingPublicKeyWithoutClientPrivateKey(t *testing.T) {
	key, err := wgtypes.GeneratePrivateKey()
	require.NoError(t, err)
	peer := Peer{Identifier: key.PublicKey().String(), PublicKey: key.PublicKey().String(), InterfaceIdentifier: "wg0", Mode: "client", Addresses: []string{"10.77.0.2/32"}}
	v := validator.New()
	v.SetTagName("binding")
	require.NoError(t, v.Struct(peer))
	require.Empty(t, NewDomainPeer(&peer).Interface.PrivateKey)
}

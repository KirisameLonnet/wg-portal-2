package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fedor-git/wg-portal-2/internal/domain"
	"github.com/stretchr/testify/require"
)

type syncPeerService struct {
	PeerService
	err     error
	applied bool
}

func (s *syncPeerService) SyncPeer(context.Context, domain.PeerIdentifier) error {
	if s.err != nil {
		return s.err
	}
	s.applied = true
	return nil
}
func TestPeerSyncAcknowledgesApplicationAndReportsErrors(t *testing.T) {
	for _, failed := range []bool{false, true} {
		service := &syncPeerService{}
		if failed {
			service.err = errors.New("kernel update failed")
		}
		endpoint := PeerEndpoint{peers: service}
		response := httptest.NewRecorder()
		endpoint.handleSyncPost()(response, httptest.NewRequest(http.MethodPost, "/sync?peer_id=peer", nil))
		if failed {
			require.Equal(t, http.StatusInternalServerError, response.Code)
			require.False(t, service.applied)
		} else {
			require.Equal(t, http.StatusOK, response.Code)
			require.True(t, service.applied)
		}
	}
}

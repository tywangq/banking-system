package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	mockdb "github.com/tywangq/banking-system/db/mock"
)

// Liveness must stay up even when the database is gone -- that is the whole reason it
// is a separate endpoint from readiness. If this ever starts calling Ping, a Postgres
// outage restarts every replica.
func TestLivenessIgnoresTheDatabase(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	store := mockdb.NewMockStore(ctrl)
	store.EXPECT().Ping(gomock.Any()).Times(0)

	server := newTestServer(t, store)
	recorder := httptest.NewRecorder()

	request, err := http.NewRequest(http.MethodGet, "/health/live", nil)
	require.NoError(t, err)
	server.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "ok")
}

func TestReadinessReflectsTheDatabase(t *testing.T) {
	testCases := []struct {
		name       string
		pingErr    error
		wantStatus int
	}{
		{name: "DatabaseReachable", pingErr: nil, wantStatus: http.StatusOK},
		// 503 rather than 500: the pod is fine, its dependency is not, and Kubernetes
		// should pull it from the Service instead of restarting it.
		{name: "DatabaseUnreachable", pingErr: sql.ErrConnDone, wantStatus: http.StatusServiceUnavailable},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			store := mockdb.NewMockStore(ctrl)
			store.EXPECT().Ping(gomock.Any()).Times(1).Return(tc.pingErr)

			server := newTestServer(t, store)
			recorder := httptest.NewRecorder()

			request, err := http.NewRequest(http.MethodGet, "/health/ready", nil)
			require.NoError(t, err)
			server.router.ServeHTTP(recorder, request)

			require.Equal(t, tc.wantStatus, recorder.Code)
		})
	}
}

// The kubelet sends no credentials, so a probe behind auth would report the pod
// unhealthy for the wrong reason.
func TestHealthEndpointsNeedNoAuth(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	store := mockdb.NewMockStore(ctrl)
	store.EXPECT().Ping(gomock.Any()).AnyTimes().Return(nil)

	server := newTestServer(t, store)

	for _, path := range []string{"/health/live", "/health/ready"} {
		recorder := httptest.NewRecorder()
		request, err := http.NewRequest(http.MethodGet, path, nil)
		require.NoError(t, err)
		server.router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code, "%s must not require authorization", path)
	}
}

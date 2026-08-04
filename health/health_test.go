package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubPinger struct {
	err    error
	called int
}

func (s *stubPinger) Ping(ctx context.Context) error {
	s.called++
	return s.err
}

// Liveness must never consult the database. If it does, a Postgres outage fails the
// probe on every replica at once and Kubernetes restarts them all.
func TestLiveDoesNotTouchTheDatabase(t *testing.T) {
	recorder := httptest.NewRecorder()
	Live(recorder, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	require.Equal(t, http.StatusOK, recorder.Code)

	var rsp Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &rsp))
	require.Equal(t, StatusLive, rsp.Status)
	require.Empty(t, rsp.Error)
}

func TestReadyWhenDatabaseIsReachable(t *testing.T) {
	pinger := &stubPinger{}

	recorder := httptest.NewRecorder()
	Ready(pinger)(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, pinger.called)

	var rsp Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &rsp))
	require.Equal(t, StatusReady, rsp.Status)
}

// 503 and not 500: the process is healthy, its dependency is not. The distinction is
// what tells Kubernetes to remove the pod from the Service rather than restart it.
func TestReadyWhenDatabaseIsUnreachable(t *testing.T) {
	pinger := &stubPinger{err: errors.New("driver: bad connection")}

	recorder := httptest.NewRecorder()
	Ready(pinger)(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)

	var rsp Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &rsp))
	require.Equal(t, StatusUnavailable, rsp.Status)
	require.Contains(t, rsp.Error, "bad connection")
}

func TestBothEndpointsReturnJSON(t *testing.T) {
	pinger := &stubPinger{}

	for name, handler := range map[string]http.HandlerFunc{
		"live":  Live,
		"ready": Ready(pinger),
	} {
		recorder := httptest.NewRecorder()
		handler(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

		require.Equal(t, "application/json; charset=utf-8",
			recorder.Header().Get("Content-Type"), "%s must declare JSON", name)
	}
}

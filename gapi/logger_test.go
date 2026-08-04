package gapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// captureLog swaps the global zerolog logger for one writing to a buffer, so a
// test can assert on what was actually logged rather than only that the code ran.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	buf := &bytes.Buffer{}
	original := log.Logger
	log.Logger = zerolog.New(buf)
	t.Cleanup(func() { log.Logger = original })

	return buf
}

func decodeLog(t *testing.T, buf *bytes.Buffer) map[string]interface{} {
	t.Helper()

	entry := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	return entry
}

func TestGrpcLoggerSuccess(t *testing.T) {
	buf := captureLog(t)

	info := &grpc.UnaryServerInfo{FullMethod: "/pb.Bank/LoginUser"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return "ok", nil
	}

	res, err := GrpcLogger(context.Background(), "req", info, handler)
	require.NoError(t, err)
	require.Equal(t, "ok", res)

	entry := decodeLog(t, buf)
	require.Equal(t, "grpc", entry["protocol"])
	require.Equal(t, "/pb.Bank/LoginUser", entry["method"])
	require.Equal(t, float64(codes.OK), entry["status_code"])
	require.Equal(t, zerolog.LevelInfoValue, entry["level"])
}

// A failing RPC must log at error level with the gRPC code, otherwise the logs
// are useless for finding which calls broke.
func TestGrpcLoggerError(t *testing.T) {
	buf := captureLog(t)

	info := &grpc.UnaryServerInfo{FullMethod: "/pb.Bank/UpdateUser"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return nil, status.Error(codes.Unauthenticated, "no token")
	}

	res, err := GrpcLogger(context.Background(), "req", info, handler)
	require.Error(t, err)
	require.Nil(t, res)

	entry := decodeLog(t, buf)
	require.Equal(t, zerolog.LevelErrorValue, entry["level"])
	require.Equal(t, float64(codes.Unauthenticated), entry["status_code"])
	require.Equal(t, codes.Unauthenticated.String(), entry["status_text"])
}

func TestHttpLoggerSuccess(t *testing.T) {
	buf := captureLog(t)

	handler := HttpLogger(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusOK)
		_, _ = res.Write([]byte(`{"ok":true}`))
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/accounts", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, `{"ok":true}`, recorder.Body.String())

	entry := decodeLog(t, buf)
	require.Equal(t, "http", entry["protocol"])
	require.Equal(t, http.MethodGet, entry["method"])
	require.Equal(t, "/v1/accounts", entry["path"])
	require.Equal(t, float64(http.StatusOK), entry["status_code"])
	require.Equal(t, zerolog.LevelInfoValue, entry["level"])

	// The response body is only logged on failure -- a success path that logged
	// every payload would leak user data into the logs.
	require.NotContains(t, buf.String(), `"body"`)
}

// On a non-200 the recorder has to capture both the status and the body, and the
// body has to still reach the client.
func TestHttpLoggerErrorCapturesBody(t *testing.T) {
	buf := captureLog(t)

	handler := HttpLogger(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusBadRequest)
		_, _ = res.Write([]byte(`{"error":"bad input"}`))
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/create_user", nil))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, `{"error":"bad input"}`, recorder.Body.String())

	entry := decodeLog(t, buf)
	require.Equal(t, zerolog.LevelErrorValue, entry["level"])
	require.Equal(t, float64(http.StatusBadRequest), entry["status_code"])
	require.Equal(t, http.StatusText(http.StatusBadRequest), entry["status_text"])
	require.Contains(t, buf.String(), "bad input")
}

// A handler that writes a body without calling WriteHeader still produces 200,
// which is what the recorder is pre-seeded with.
func TestHttpLoggerDefaultsToOK(t *testing.T) {
	buf := captureLog(t)

	handler := HttpLogger(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		_, _ = res.Write([]byte("hello"))
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil))

	require.Equal(t, http.StatusOK, recorder.Code)

	entry := decodeLog(t, buf)
	require.Equal(t, float64(http.StatusOK), entry["status_code"])
	require.Equal(t, zerolog.LevelInfoValue, entry["level"])
}

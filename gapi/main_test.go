package gapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	db "github.com/tywangq/banking-system/db/sqlc"
	"github.com/tywangq/banking-system/token"
	"github.com/tywangq/banking-system/util"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func newTestServer(t *testing.T, store db.Store) *Server {
	config := util.Config{
		TokenSymmetricKey:    util.RandomString(32),
		AccessTokenDuration:  time.Minute,
		RefreshTokenDuration: time.Hour,
	}

	server, err := NewServer(config, store)
	require.NoError(t, err)

	return server
}

// newContextWithBearerToken mints a token and attaches it as incoming metadata,
// which is how authorizeUser sees it in a real request. A negative duration
// yields an already-expired token.
func newContextWithBearerToken(t *testing.T, tokenMaker token.Maker, username string, duration time.Duration) context.Context {
	accessToken, payload, err := tokenMaker.CreateToken(username, duration)
	require.NoError(t, err)
	require.NotEmpty(t, payload)

	bearerToken := fmt.Sprintf("%s %s", authorizationBearer, accessToken)
	md := metadata.MD{
		authorizationHeader: []string{bearerToken},
	}
	return metadata.NewIncomingContext(context.Background(), md)
}

// requireGRPCCode asserts the handler returned a gRPC status error with the
// expected code. Codes are the contract for gRPC callers the same way HTTP
// status codes are for the REST handlers.
func requireGRPCCode(t *testing.T, err error, code codes.Code) {
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "error is not a gRPC status: %v", err)
	require.Equal(t, code, st.Code())
}

func randomUser(t *testing.T) (user db.User, password string) {
	password = util.RandomString(6)
	hashedPassword, err := util.HashPassword(password)
	require.NoError(t, err)
	require.NotEmpty(t, hashedPassword)

	user = db.User{
		Username:       util.RandomOwner(),
		HashedPassword: hashedPassword,
		FullName:       util.RandomOwner(),
		Email:          util.RandomEmail(),
	}
	return
}

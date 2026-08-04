package gapi

import (
	"context"
	"database/sql"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	mockdb "github.com/tywangq/banking-system/db/mock"
	db "github.com/tywangq/banking-system/db/sqlc"
	"github.com/tywangq/banking-system/pb"
	"github.com/tywangq/banking-system/util"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

func TestLoginUserRPC(t *testing.T) {
	user, password := randomUser(t)

	testCases := []struct {
		name          string
		req           *pb.LoginUserRequest
		buildStubs    func(store *mockdb.MockStore)
		checkResponse func(t *testing.T, server *Server, res *pb.LoginUserResponse, err error)
	}{
		{
			name: "OK",
			req: &pb.LoginUserRequest{
				Username: user.Username,
				Password: password,
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().
					GetUser(gomock.Any(), gomock.Eq(user.Username)).
					Times(1).
					Return(user, nil)
				store.EXPECT().
					CreateSession(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.Session{ID: uuid.New(), Username: user.Username}, nil)
			},
			checkResponse: func(t *testing.T, server *Server, res *pb.LoginUserResponse, err error) {
				require.NoError(t, err)
				require.NotNil(t, res)
				require.Equal(t, user.Username, res.GetUser().GetUsername())
				require.NotEmpty(t, res.GetSessionId())

				// Both tokens must be real, verifiable tokens for this user --
				// a non-empty string is not enough of an assertion.
				accessPayload, err := server.tokenMaker.VerifyToken(res.GetAccessToken())
				require.NoError(t, err)
				require.Equal(t, user.Username, accessPayload.Username)

				refreshPayload, err := server.tokenMaker.VerifyToken(res.GetRefreshToken())
				require.NoError(t, err)
				require.Equal(t, user.Username, refreshPayload.Username)

				// The refresh token has to outlive the access token, or renewal
				// is pointless.
				require.True(t, refreshPayload.ExpiredAt.After(accessPayload.ExpiredAt))
			},
		},
		{
			name: "UserNotFound",
			req: &pb.LoginUserRequest{
				Username: user.Username,
				Password: password,
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().
					GetUser(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.User{}, sql.ErrNoRows)
				store.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, server *Server, res *pb.LoginUserResponse, err error) {
				requireGRPCCode(t, err, codes.NotFound)
			},
		},
		{
			// A wrong password must not mint a token, and must not be reported
			// as anything the caller can retry.
			name: "IncorrectPassword",
			req: &pb.LoginUserRequest{
				Username: user.Username,
				Password: util.RandomString(6),
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().
					GetUser(gomock.Any(), gomock.Eq(user.Username)).
					Times(1).
					Return(user, nil)
				store.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, server *Server, res *pb.LoginUserResponse, err error) {
				requireGRPCCode(t, err, codes.PermissionDenied)
				require.Nil(t, res)
			},
		},
		{
			name: "GetUserInternalError",
			req: &pb.LoginUserRequest{
				Username: user.Username,
				Password: password,
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().
					GetUser(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.User{}, sql.ErrConnDone)
				store.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, server *Server, res *pb.LoginUserResponse, err error) {
				requireGRPCCode(t, err, codes.Internal)
			},
		},
		{
			// The password is verified before the session row is written, so a
			// failure here must not leave the caller believing it logged in.
			name: "CreateSessionError",
			req: &pb.LoginUserRequest{
				Username: user.Username,
				Password: password,
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().
					GetUser(gomock.Any(), gomock.Eq(user.Username)).
					Times(1).
					Return(user, nil)
				store.EXPECT().
					CreateSession(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.Session{}, sql.ErrConnDone)
			},
			checkResponse: func(t *testing.T, server *Server, res *pb.LoginUserResponse, err error) {
				requireGRPCCode(t, err, codes.Internal)
				require.Nil(t, res)
			},
		},
		{
			name: "InvalidUsername",
			req: &pb.LoginUserRequest{
				Username: "invalid-user#1",
				Password: password,
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().GetUser(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, server *Server, res *pb.LoginUserResponse, err error) {
				requireGRPCCode(t, err, codes.InvalidArgument)
			},
		},
	}

	for i := range testCases {
		tc := testCases[i]

		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			store := mockdb.NewMockStore(ctrl)
			tc.buildStubs(store)

			server := newTestServer(t, store)
			res, err := server.LoginUser(context.Background(), tc.req)
			tc.checkResponse(t, server, res, err)
		})
	}
}

// TestLoginUserRecordsMetadata covers extractMetadata: the user agent and client
// IP stored on the session come from gRPC metadata, and are what makes a session
// auditable later.
func TestLoginUserRecordsMetadata(t *testing.T) {
	user, password := randomUser(t)

	const (
		userAgent = "grpc-go/1.70.0"
		clientIP  = "203.0.113.7"
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	store := mockdb.NewMockStore(ctrl)
	store.EXPECT().
		GetUser(gomock.Any(), gomock.Eq(user.Username)).
		Times(1).
		Return(user, nil)

	var got db.CreateSessionParams
	store.EXPECT().
		CreateSession(gomock.Any(), gomock.Any()).
		Times(1).
		DoAndReturn(func(_ context.Context, arg db.CreateSessionParams) (db.Session, error) {
			got = arg
			return db.Session{ID: arg.ID, Username: arg.Username}, nil
		})

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{
		grpcGatewayUserAgentHeader: []string{userAgent},
		xForwardedForHeader:        []string{clientIP},
	})

	server := newTestServer(t, store)
	res, err := server.LoginUser(ctx, &pb.LoginUserRequest{
		Username: user.Username,
		Password: password,
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.GetSessionId())

	require.Equal(t, userAgent, got.UserAgent)
	require.Equal(t, clientIP, got.ClientIp)
	require.Equal(t, user.Username, got.Username)
	require.False(t, got.IsBlocked)
	require.Equal(t, res.GetRefreshToken(), got.RefreshToken)
}

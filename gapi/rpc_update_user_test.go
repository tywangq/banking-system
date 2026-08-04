package gapi

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	mockdb "github.com/tywangq/banking-system/db/mock"
	db "github.com/tywangq/banking-system/db/sqlc"
	"github.com/tywangq/banking-system/pb"
	"github.com/tywangq/banking-system/token"
	"github.com/tywangq/banking-system/util"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

func ptr(s string) *string { return &s }

func TestUpdateUserRPC(t *testing.T) {
	user, _ := randomUser(t)
	other, _ := randomUser(t)

	newName := util.RandomOwner()
	newEmail := util.RandomEmail()

	testCases := []struct {
		name          string
		req           *pb.UpdateUserRequest
		buildContext  func(t *testing.T, tokenMaker token.Maker) context.Context
		buildStubs    func(store *mockdb.MockStore)
		checkResponse func(t *testing.T, res *pb.UpdateUserResponse, err error)
	}{
		{
			name: "OK",
			req: &pb.UpdateUserRequest{
				Username: user.Username,
				FullName: ptr(newName),
				Email:    ptr(newEmail),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				return newContextWithBearerToken(t, tokenMaker, user.Username, time.Minute)
			},
			buildStubs: func(store *mockdb.MockStore) {
				arg := db.UpdateUserParams{
					Username: user.Username,
					FullName: sql.NullString{String: newName, Valid: true},
					Email:    sql.NullString{String: newEmail, Valid: true},
				}
				updated := user
				updated.FullName = newName
				updated.Email = newEmail

				store.EXPECT().
					UpdateUser(gomock.Any(), gomock.Eq(arg)).
					Times(1).
					Return(updated, nil)
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
				require.NoError(t, err)
				require.NotNil(t, res)
				require.Equal(t, user.Username, res.GetUser().GetUsername())
				require.Equal(t, newName, res.GetUser().GetFullName())
				require.Equal(t, newEmail, res.GetUser().GetEmail())
			},
		},
		{
			// Omitted fields must stay omitted rather than being written as
			// empty strings -- that is the whole point of the optional/NullString
			// pairing, and the easiest thing to get wrong.
			name: "PartialUpdateLeavesOtherFieldsUntouched",
			req: &pb.UpdateUserRequest{
				Username: user.Username,
				Email:    ptr(newEmail),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				return newContextWithBearerToken(t, tokenMaker, user.Username, time.Minute)
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().
					UpdateUser(gomock.Any(), gomock.Any()).
					Times(1).
					DoAndReturn(func(_ context.Context, arg db.UpdateUserParams) (db.User, error) {
						require.True(t, arg.Email.Valid)
						require.Equal(t, newEmail, arg.Email.String)
						require.False(t, arg.FullName.Valid)
						require.False(t, arg.HashedPassword.Valid)
						require.False(t, arg.PasswordChangedAt.Valid)
						return user, nil
					})
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
				require.NoError(t, err)
			},
		},
		{
			// A password change must be hashed, must never be stored in the
			// clear, and must stamp PasswordChangedAt.
			name: "PasswordIsHashedAndTimestamped",
			req: &pb.UpdateUserRequest{
				Username: user.Username,
				Password: ptr("new-secret-1"),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				return newContextWithBearerToken(t, tokenMaker, user.Username, time.Minute)
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().
					UpdateUser(gomock.Any(), gomock.Any()).
					Times(1).
					DoAndReturn(func(_ context.Context, arg db.UpdateUserParams) (db.User, error) {
						require.True(t, arg.HashedPassword.Valid)
						require.NotEqual(t, "new-secret-1", arg.HashedPassword.String)
						require.NoError(t, util.CheckPassword("new-secret-1", arg.HashedPassword.String))
						require.True(t, arg.PasswordChangedAt.Valid)
						require.WithinDuration(t, time.Now(), arg.PasswordChangedAt.Time, time.Minute)
						return user, nil
					})
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
				require.NoError(t, err)
			},
		},
		{
			// The one that actually matters: a valid token for one user must not
			// authorize editing another user.
			name: "CannotUpdateOtherUser",
			req: &pb.UpdateUserRequest{
				Username: other.Username,
				FullName: ptr(newName),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				return newContextWithBearerToken(t, tokenMaker, user.Username, time.Minute)
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
				requireGRPCCode(t, err, codes.PermissionDenied)
				require.Nil(t, res)
			},
		},
		{
			name: "ExpiredToken",
			req: &pb.UpdateUserRequest{
				Username: user.Username,
				FullName: ptr(newName),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				return newContextWithBearerToken(t, tokenMaker, user.Username, -time.Minute)
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
				requireGRPCCode(t, err, codes.Unauthenticated)
			},
		},
		{
			name: "NoAuthorization",
			req: &pb.UpdateUserRequest{
				Username: user.Username,
				FullName: ptr(newName),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				return context.Background()
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
				requireGRPCCode(t, err, codes.Unauthenticated)
			},
		},
		{
			name: "MissingAuthorizationHeader",
			req: &pb.UpdateUserRequest{
				Username: user.Username,
				FullName: ptr(newName),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				return metadata.NewIncomingContext(context.Background(), metadata.MD{})
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
				requireGRPCCode(t, err, codes.Unauthenticated)
			},
		},
		{
			name: "MalformedAuthorizationHeader",
			req: &pb.UpdateUserRequest{
				Username: user.Username,
				FullName: ptr(newName),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				return metadata.NewIncomingContext(context.Background(), metadata.MD{
					authorizationHeader: []string{"only-one-field"},
				})
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
				requireGRPCCode(t, err, codes.Unauthenticated)
			},
		},
		{
			name: "UnsupportedAuthorizationType",
			req: &pb.UpdateUserRequest{
				Username: user.Username,
				FullName: ptr(newName),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				return metadata.NewIncomingContext(context.Background(), metadata.MD{
					authorizationHeader: []string{"basic dXNlcjpwYXNz"},
				})
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
				requireGRPCCode(t, err, codes.Unauthenticated)
			},
		},
		{
			// A token signed by a different key must not be accepted, even
			// though it is a structurally valid Paseto token.
			name: "TokenFromAnotherKey",
			req: &pb.UpdateUserRequest{
				Username: user.Username,
				FullName: ptr(newName),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				foreign, err := token.NewPasetoMaker(util.RandomString(32))
				require.NoError(t, err)
				return newContextWithBearerToken(t, foreign, user.Username, time.Minute)
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
				requireGRPCCode(t, err, codes.Unauthenticated)
			},
		},
		{
			name: "UserNotFound",
			req: &pb.UpdateUserRequest{
				Username: user.Username,
				FullName: ptr(newName),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				return newContextWithBearerToken(t, tokenMaker, user.Username, time.Minute)
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().
					UpdateUser(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.User{}, sql.ErrNoRows)
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
				requireGRPCCode(t, err, codes.NotFound)
			},
		},
		{
			name: "InternalError",
			req: &pb.UpdateUserRequest{
				Username: user.Username,
				FullName: ptr(newName),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				return newContextWithBearerToken(t, tokenMaker, user.Username, time.Minute)
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().
					UpdateUser(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.User{}, sql.ErrConnDone)
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
				requireGRPCCode(t, err, codes.Internal)
			},
		},
		{
			name: "InvalidEmail",
			req: &pb.UpdateUserRequest{
				Username: user.Username,
				Email:    ptr("not-an-email"),
			},
			buildContext: func(t *testing.T, tokenMaker token.Maker) context.Context {
				return newContextWithBearerToken(t, tokenMaker, user.Username, time.Minute)
			},
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, res *pb.UpdateUserResponse, err error) {
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
			ctx := tc.buildContext(t, server.tokenMaker)

			res, err := server.UpdateUser(ctx, tc.req)
			tc.checkResponse(t, res, err)
		})
	}
}

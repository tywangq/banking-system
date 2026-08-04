package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	mockdb "github.com/tywangq/banking-system/db/mock"
	db "github.com/tywangq/banking-system/db/sqlc"
	"github.com/tywangq/banking-system/token"
)

func TestLogoutAPI(t *testing.T) {
	user, _ := randomUser(t)
	other, _ := randomUser(t)

	testCases := []struct {
		name          string
		setupRequest  func(t *testing.T, tokenMaker token.Maker) (gin.H, uuid.UUID)
		buildStubs    func(store *mockdb.MockStore, sessionID uuid.UUID)
		setupAuth     func(t *testing.T, request *http.Request, tokenMaker token.Maker)
		checkResponse func(t *testing.T, recorder *httptest.ResponseRecorder)
	}{
		{
			name: "OK",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, uuid.UUID) {
				refreshToken, payload, err := tokenMaker.CreateToken(user.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, payload.ID
			},
			buildStubs: func(store *mockdb.MockStore, sessionID uuid.UUID) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Eq(sessionID)).Times(1).
					Return(db.Session{ID: sessionID, Username: user.Username, IsBlocked: false}, nil)
				store.EXPECT().BlockSession(gomock.Any(), gomock.Eq(sessionID)).Times(1).
					Return(db.Session{ID: sessionID, Username: user.Username, IsBlocked: true}, nil)
			},
			setupAuth: func(t *testing.T, request *http.Request, tokenMaker token.Maker) {
				addAuthorization(t, request, tokenMaker, authorizationTypeBearer, user.Username, time.Minute)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusOK, recorder.Code)
			},
		},
		{
			// The security case: holding a refresh token for someone else's session
			// must not let you revoke it.
			name: "CannotRevokeAnotherUsersSession",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, uuid.UUID) {
				refreshToken, payload, err := tokenMaker.CreateToken(other.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, payload.ID
			},
			buildStubs: func(store *mockdb.MockStore, sessionID uuid.UUID) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Eq(sessionID)).Times(1).
					Return(db.Session{ID: sessionID, Username: other.Username}, nil)
				store.EXPECT().BlockSession(gomock.Any(), gomock.Any()).Times(0)
			},
			setupAuth: func(t *testing.T, request *http.Request, tokenMaker token.Maker) {
				addAuthorization(t, request, tokenMaker, authorizationTypeBearer, user.Username, time.Minute)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusUnauthorized, recorder.Code)
			},
		},
		{
			// Signing out twice is not an error, so a retry after a dropped response
			// succeeds instead of confusing the client.
			name: "AlreadyBlockedIsIdempotent",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, uuid.UUID) {
				refreshToken, payload, err := tokenMaker.CreateToken(user.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, payload.ID
			},
			buildStubs: func(store *mockdb.MockStore, sessionID uuid.UUID) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Eq(sessionID)).Times(1).
					Return(db.Session{ID: sessionID, Username: user.Username, IsBlocked: true}, nil)
				store.EXPECT().BlockSession(gomock.Any(), gomock.Any()).Times(0)
			},
			setupAuth: func(t *testing.T, request *http.Request, tokenMaker token.Maker) {
				addAuthorization(t, request, tokenMaker, authorizationTypeBearer, user.Username, time.Minute)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusOK, recorder.Code)
			},
		},
		{
			name: "SessionNotFound",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, uuid.UUID) {
				refreshToken, payload, err := tokenMaker.CreateToken(user.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, payload.ID
			},
			buildStubs: func(store *mockdb.MockStore, sessionID uuid.UUID) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Any()).Times(1).
					Return(db.Session{}, sql.ErrNoRows)
				store.EXPECT().BlockSession(gomock.Any(), gomock.Any()).Times(0)
			},
			setupAuth: func(t *testing.T, request *http.Request, tokenMaker token.Maker) {
				addAuthorization(t, request, tokenMaker, authorizationTypeBearer, user.Username, time.Minute)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusNotFound, recorder.Code)
			},
		},
		{
			name: "InvalidRefreshToken",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, uuid.UUID) {
				return gin.H{"refresh_token": "not-a-token"}, uuid.New()
			},
			buildStubs: func(store *mockdb.MockStore, sessionID uuid.UUID) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Any()).Times(0)
				store.EXPECT().BlockSession(gomock.Any(), gomock.Any()).Times(0)
			},
			setupAuth: func(t *testing.T, request *http.Request, tokenMaker token.Maker) {
				addAuthorization(t, request, tokenMaker, authorizationTypeBearer, user.Username, time.Minute)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusUnauthorized, recorder.Code)
			},
		},
		{
			name: "NoAuthorization",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, uuid.UUID) {
				refreshToken, payload, err := tokenMaker.CreateToken(user.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, payload.ID
			},
			buildStubs: func(store *mockdb.MockStore, sessionID uuid.UUID) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Any()).Times(0)
				store.EXPECT().BlockSession(gomock.Any(), gomock.Any()).Times(0)
			},
			setupAuth: func(t *testing.T, request *http.Request, tokenMaker token.Maker) {},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusUnauthorized, recorder.Code)
			},
		},
		{
			name: "BlockSessionError",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, uuid.UUID) {
				refreshToken, payload, err := tokenMaker.CreateToken(user.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, payload.ID
			},
			buildStubs: func(store *mockdb.MockStore, sessionID uuid.UUID) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Any()).Times(1).
					Return(db.Session{ID: sessionID, Username: user.Username}, nil)
				store.EXPECT().BlockSession(gomock.Any(), gomock.Any()).Times(1).
					Return(db.Session{}, sql.ErrConnDone)
			},
			setupAuth: func(t *testing.T, request *http.Request, tokenMaker token.Maker) {
				addAuthorization(t, request, tokenMaker, authorizationTypeBearer, user.Username, time.Minute)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusInternalServerError, recorder.Code)
			},
		},
	}

	for i := range testCases {
		tc := testCases[i]

		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			store := mockdb.NewMockStore(ctrl)
			server := newTestServer(t, store)

			body, sessionID := tc.setupRequest(t, server.tokenMaker)
			tc.buildStubs(store, sessionID)

			data, err := json.Marshal(body)
			require.NoError(t, err)

			request, err := http.NewRequest(http.MethodPost, "/users/logout", bytes.NewReader(data))
			require.NoError(t, err)

			tc.setupAuth(t, request, server.tokenMaker)
			recorder := httptest.NewRecorder()
			server.router.ServeHTTP(recorder, request)
			tc.checkResponse(t, recorder)
		})
	}
}

func TestLogoutAllAPI(t *testing.T) {
	user, _ := randomUser(t)

	testCases := []struct {
		name          string
		buildStubs    func(store *mockdb.MockStore)
		setupAuth     func(t *testing.T, request *http.Request, tokenMaker token.Maker)
		checkResponse func(t *testing.T, recorder *httptest.ResponseRecorder)
	}{
		{
			// Scoped to the authenticated user, and it reports how many were open so
			// the caller learns something it could not otherwise see.
			name: "RevokesOnlyTheCallersSessions",
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().
					BlockUserSessions(gomock.Any(), gomock.Eq(user.Username)).
					Times(1).
					Return(int64(3), nil)
			},
			setupAuth: func(t *testing.T, request *http.Request, tokenMaker token.Maker) {
				addAuthorization(t, request, tokenMaker, authorizationTypeBearer, user.Username, time.Minute)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusOK, recorder.Code)
				var rsp logoutAllResponse
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &rsp))
				require.Equal(t, int64(3), rsp.SessionsRevoked)
			},
		},
		{
			// Nothing open is still a success -- signing out when already signed out
			// everywhere is not a failure.
			name: "NoOpenSessionsStillSucceeds",
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().BlockUserSessions(gomock.Any(), gomock.Any()).Times(1).Return(int64(0), nil)
			},
			setupAuth: func(t *testing.T, request *http.Request, tokenMaker token.Maker) {
				addAuthorization(t, request, tokenMaker, authorizationTypeBearer, user.Username, time.Minute)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusOK, recorder.Code)
			},
		},
		{
			name: "NoAuthorization",
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().BlockUserSessions(gomock.Any(), gomock.Any()).Times(0)
			},
			setupAuth: func(t *testing.T, request *http.Request, tokenMaker token.Maker) {},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusUnauthorized, recorder.Code)
			},
		},
		{
			name: "InternalError",
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().BlockUserSessions(gomock.Any(), gomock.Any()).Times(1).Return(int64(0), sql.ErrConnDone)
			},
			setupAuth: func(t *testing.T, request *http.Request, tokenMaker token.Maker) {
				addAuthorization(t, request, tokenMaker, authorizationTypeBearer, user.Username, time.Minute)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusInternalServerError, recorder.Code)
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
			request, err := http.NewRequest(http.MethodPost, "/users/logout_all", nil)
			require.NoError(t, err)

			tc.setupAuth(t, request, server.tokenMaker)
			recorder := httptest.NewRecorder()
			server.router.ServeHTTP(recorder, request)
			tc.checkResponse(t, recorder)
		})
	}
}

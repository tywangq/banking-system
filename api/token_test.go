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
	"github.com/stretchr/testify/require"
	mockdb "github.com/tywangq/banking-system/db/mock"
	db "github.com/tywangq/banking-system/db/sqlc"
	"github.com/tywangq/banking-system/token"
)

// renewAccessToken is where session revocation is actually enforced: blocking a
// session only matters if refresh refuses it afterwards. These cases close that loop.
func TestRenewAccessTokenAPI(t *testing.T) {
	user, _ := randomUser(t)
	other, _ := randomUser(t)

	testCases := []struct {
		name          string
		setupRequest  func(t *testing.T, tokenMaker token.Maker) (gin.H, db.Session)
		buildStubs    func(store *mockdb.MockStore, session db.Session)
		checkResponse func(t *testing.T, recorder *httptest.ResponseRecorder)
	}{
		{
			name: "OK",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, db.Session) {
				refreshToken, payload, err := tokenMaker.CreateToken(user.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, db.Session{
					ID:           payload.ID,
					Username:     user.Username,
					RefreshToken: refreshToken,
					ExpiresAt:    time.Now().Add(time.Hour),
				}
			},
			buildStubs: func(store *mockdb.MockStore, session db.Session) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Eq(session.ID)).Times(1).Return(session, nil)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusOK, recorder.Code)
				var rsp renewAccessTokenResponse
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &rsp))
				require.NotEmpty(t, rsp.AccessToken)
			},
		},
		{
			// The point of logout. A revoked session must not be able to mint a new
			// access token, no matter that its refresh token is still cryptographically
			// valid and unexpired.
			name: "BlockedSessionCannotRefresh",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, db.Session) {
				refreshToken, payload, err := tokenMaker.CreateToken(user.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, db.Session{
					ID:           payload.ID,
					Username:     user.Username,
					RefreshToken: refreshToken,
					IsBlocked:    true,
					ExpiresAt:    time.Now().Add(time.Hour),
				}
			},
			buildStubs: func(store *mockdb.MockStore, session db.Session) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Eq(session.ID)).Times(1).Return(session, nil)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusUnauthorized, recorder.Code)
				require.Contains(t, recorder.Body.String(), "blocked session")
			},
		},
		{
			// A token whose session belongs to somebody else must not refresh, which
			// guards against a session row being swapped underneath a valid token.
			name: "SessionUserMismatch",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, db.Session) {
				refreshToken, payload, err := tokenMaker.CreateToken(user.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, db.Session{
					ID:           payload.ID,
					Username:     other.Username,
					RefreshToken: refreshToken,
					ExpiresAt:    time.Now().Add(time.Hour),
				}
			},
			buildStubs: func(store *mockdb.MockStore, session db.Session) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Any()).Times(1).Return(session, nil)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusUnauthorized, recorder.Code)
			},
		},
		{
			// The stored token has to match the presented one, so an old refresh token
			// for a recycled session ID cannot be replayed.
			name: "StoredTokenMismatch",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, db.Session) {
				refreshToken, payload, err := tokenMaker.CreateToken(user.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, db.Session{
					ID:           payload.ID,
					Username:     user.Username,
					RefreshToken: "a-different-token",
					ExpiresAt:    time.Now().Add(time.Hour),
				}
			},
			buildStubs: func(store *mockdb.MockStore, session db.Session) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Any()).Times(1).Return(session, nil)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusUnauthorized, recorder.Code)
			},
		},
		{
			name: "ExpiredSession",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, db.Session) {
				refreshToken, payload, err := tokenMaker.CreateToken(user.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, db.Session{
					ID:           payload.ID,
					Username:     user.Username,
					RefreshToken: refreshToken,
					ExpiresAt:    time.Now().Add(-time.Minute),
				}
			},
			buildStubs: func(store *mockdb.MockStore, session db.Session) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Any()).Times(1).Return(session, nil)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusUnauthorized, recorder.Code)
			},
		},
		{
			name: "ExpiredRefreshToken",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, db.Session) {
				refreshToken, _, err := tokenMaker.CreateToken(user.Username, -time.Minute)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, db.Session{}
			},
			buildStubs: func(store *mockdb.MockStore, session db.Session) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusUnauthorized, recorder.Code)
			},
		},
		{
			name: "SessionNotFound",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, db.Session) {
				refreshToken, payload, err := tokenMaker.CreateToken(user.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, db.Session{ID: payload.ID}
			},
			buildStubs: func(store *mockdb.MockStore, session db.Session) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Any()).Times(1).Return(db.Session{}, sql.ErrNoRows)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusNotFound, recorder.Code)
			},
		},
		{
			name: "InternalError",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, db.Session) {
				refreshToken, payload, err := tokenMaker.CreateToken(user.Username, time.Hour)
				require.NoError(t, err)
				return gin.H{"refresh_token": refreshToken}, db.Session{ID: payload.ID}
			},
			buildStubs: func(store *mockdb.MockStore, session db.Session) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Any()).Times(1).Return(db.Session{}, sql.ErrConnDone)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusInternalServerError, recorder.Code)
			},
		},
		{
			name: "MissingRefreshToken",
			setupRequest: func(t *testing.T, tokenMaker token.Maker) (gin.H, db.Session) {
				return gin.H{}, db.Session{}
			},
			buildStubs: func(store *mockdb.MockStore, session db.Session) {
				store.EXPECT().GetSession(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusBadRequest, recorder.Code)
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

			body, session := tc.setupRequest(t, server.tokenMaker)
			tc.buildStubs(store, session)

			data, err := json.Marshal(body)
			require.NoError(t, err)

			request, err := http.NewRequest(http.MethodPost, "/tokens/renew_access", bytes.NewReader(data))
			require.NoError(t, err)

			recorder := httptest.NewRecorder()
			server.router.ServeHTTP(recorder, request)
			tc.checkResponse(t, recorder)
		})
	}
}

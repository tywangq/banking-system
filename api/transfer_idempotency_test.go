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
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	mockdb "github.com/tywangq/banking-system/db/mock"
	db "github.com/tywangq/banking-system/db/sqlc"
	"github.com/tywangq/banking-system/util"
)

func TestCreateTransferIdempotency(t *testing.T) {
	amount := int64(10)

	user1, _ := randomUser(t)
	user2, _ := randomUser(t)

	account1 := randomAccount(user1.Username)
	account2 := randomAccount(user2.Username)
	account1.Currency = util.USD
	account2.Currency = util.USD

	req := transferRequest{
		FromAccountID: account1.ID,
		ToAccountID:   account2.ID,
		Amount:        amount,
		Currency:      util.USD,
	}
	body := gin.H{
		"from_account_id": account1.ID,
		"to_account_id":   account2.ID,
		"amount":          amount,
		"currency":        util.USD,
	}
	fingerprint := requestFingerprint(req)

	storedResult := db.TransferTxResult{
		Transfer:    db.Transfer{ID: 42, FromAccountID: account1.ID, ToAccountID: account2.ID, Amount: amount},
		FromAccount: account1,
		ToAccount:   account2,
	}
	storedBody, err := json.Marshal(storedResult)
	require.NoError(t, err)

	uniqueViolation := &pq.Error{Code: "23505", Constraint: "idempotency_keys_pkey"}

	testCases := []struct {
		name          string
		key           string
		buildStubs    func(store *mockdb.MockStore)
		checkResponse func(t *testing.T, recorder *httptest.ResponseRecorder)
	}{
		{
			// No header: the endpoint behaves exactly as before, via the plain path.
			name: "NoKeyUsesPlainTransfer",
			key:  "",
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account1.ID)).Times(1).Return(account1, nil)
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account2.ID)).Times(1).Return(account2, nil)
				store.EXPECT().TransferTx(gomock.Any(), gomock.Any()).Times(1)
				store.EXPECT().IdempotentTransferTx(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Empty(t, recorder.Header().Get("Idempotent-Replay"))
			},
		},
		{
			// With a header the claim and the transfer go through the one transaction,
			// and the owner and fingerprint travel with them.
			name: "FirstRequestClaimsTheKey",
			key:  "key-abc-123",
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account1.ID)).Times(1).Return(account1, nil)
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account2.ID)).Times(1).Return(account2, nil)
				store.EXPECT().TransferTx(gomock.Any(), gomock.Any()).Times(0)
				store.EXPECT().
					IdempotentTransferTx(gomock.Any(), gomock.Eq(db.IdempotentTransferTxParams{
						TransferTxParams: db.TransferTxParams{
							FromAccountID: account1.ID,
							ToAccountID:   account2.ID,
							Amount:        amount,
						},
						Owner:       user1.Username,
						Key:         "key-abc-123",
						RequestHash: fingerprint,
					})).
					Times(1).
					Return(storedResult, nil)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Empty(t, recorder.Header().Get("Idempotent-Replay"))
			},
		},
		{
			// The retry: the key is taken, so the stored response comes back verbatim
			// and no second transfer is attempted.
			name: "RetryReplaysTheStoredResponse",
			key:  "key-abc-123",
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account1.ID)).Times(1).Return(account1, nil)
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account2.ID)).Times(1).Return(account2, nil)
				store.EXPECT().
					IdempotentTransferTx(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.TransferTxResult{}, uniqueViolation)
				store.EXPECT().
					GetIdempotencyKey(gomock.Any(), gomock.Eq(db.GetIdempotencyKeyParams{
						Owner: user1.Username,
						Key:   "key-abc-123",
					})).
					Times(1).
					Return(db.IdempotencyKey{
						Owner:        user1.Username,
						Key:          "key-abc-123",
						RequestHash:  fingerprint,
						ResponseBody: string(storedBody),
					}, nil)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Equal(t, "true", recorder.Header().Get("Idempotent-Replay"))
				require.JSONEq(t, string(storedBody), recorder.Body.String())
			},
		},
		{
			// Same key, different parameters: a client bug. Replaying would answer a
			// question that was never asked.
			name: "SameKeyDifferentRequestConflicts",
			key:  "key-abc-123",
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account1.ID)).Times(1).Return(account1, nil)
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account2.ID)).Times(1).Return(account2, nil)
				store.EXPECT().
					IdempotentTransferTx(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.TransferTxResult{}, uniqueViolation)
				store.EXPECT().
					GetIdempotencyKey(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.IdempotencyKey{
						RequestHash:  "a-fingerprint-for-some-other-transfer",
						ResponseBody: string(storedBody),
					}, nil)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusConflict, recorder.Code)
				require.NotContains(t, recorder.Body.String(), `"transfer"`)
			},
		},
		{
			name: "KeyTooLong",
			key:  string(make([]byte, maxIdempotencyKeyLen+1)),
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account1.ID)).Times(1).Return(account1, nil)
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account2.ID)).Times(1).Return(account2, nil)
				store.EXPECT().IdempotentTransferTx(gomock.Any(), gomock.Any()).Times(0)
				store.EXPECT().TransferTx(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusBadRequest, recorder.Code)
			},
		},
		{
			// An overdraft on the idempotent path still has to read as a client error,
			// not get swallowed by the replay branch.
			name: "InsufficientBalanceOnIdempotentPath",
			key:  "key-xyz",
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account1.ID)).Times(1).Return(account1, nil)
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account2.ID)).Times(1).Return(account2, nil)
				store.EXPECT().
					IdempotentTransferTx(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.TransferTxResult{}, &pq.Error{
						Code:       "23514",
						Constraint: "accounts_balance_non_negative",
					})
				store.EXPECT().GetIdempotencyKey(gomock.Any(), gomock.Any()).Times(0)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusBadRequest, recorder.Code)
				require.Contains(t, recorder.Body.String(), "insufficient balance")
			},
		},
		{
			// Should be unreachable: claim and completion share a transaction, so a
			// committed row always has a body. Report a conflict rather than replaying
			// nothing.
			name: "ClaimedButEmptyBodyConflicts",
			key:  "key-partial",
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account1.ID)).Times(1).Return(account1, nil)
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account2.ID)).Times(1).Return(account2, nil)
				store.EXPECT().
					IdempotentTransferTx(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.TransferTxResult{}, uniqueViolation)
				store.EXPECT().
					GetIdempotencyKey(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.IdempotencyKey{RequestHash: fingerprint, ResponseBody: ""}, nil)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusConflict, recorder.Code)
			},
		},
		{
			name: "StoredKeyLookupFails",
			key:  "key-abc-123",
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account1.ID)).Times(1).Return(account1, nil)
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account2.ID)).Times(1).Return(account2, nil)
				store.EXPECT().
					IdempotentTransferTx(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.TransferTxResult{}, uniqueViolation)
				store.EXPECT().
					GetIdempotencyKey(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.IdempotencyKey{}, sql.ErrConnDone)
			},
			checkResponse: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusInternalServerError, recorder.Code)
			},
		},
		{
			name: "IdempotentTransferInternalError",
			key:  "key-boom",
			buildStubs: func(store *mockdb.MockStore) {
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account1.ID)).Times(1).Return(account1, nil)
				store.EXPECT().GetAccount(gomock.Any(), gomock.Eq(account2.ID)).Times(1).Return(account2, nil)
				store.EXPECT().
					IdempotentTransferTx(gomock.Any(), gomock.Any()).
					Times(1).
					Return(db.TransferTxResult{}, sql.ErrTxDone)
				store.EXPECT().GetIdempotencyKey(gomock.Any(), gomock.Any()).Times(0)
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
			recorder := httptest.NewRecorder()

			data, err := json.Marshal(body)
			require.NoError(t, err)

			request, err := http.NewRequest(http.MethodPost, "/transfers", bytes.NewReader(data))
			require.NoError(t, err)
			if tc.key != "" {
				request.Header.Set(idempotencyKeyHeader, tc.key)
			}

			addAuthorization(t, request, server.tokenMaker, authorizationTypeBearer, user1.Username, time.Minute)
			server.router.ServeHTTP(recorder, request)
			tc.checkResponse(t, recorder)
		})
	}
}

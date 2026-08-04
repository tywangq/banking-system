package db

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/tywangq/banking-system/util"
)

// TestIdempotentTransferTxMovesMoneyOnce is the case the whole feature exists for:
// the same key arriving twice must transfer once. The second call fails on the
// primary key rather than performing a second transfer.
func TestIdempotentTransferTxMovesMoneyOnce(t *testing.T) {
	store := NewStore(testDB)

	account1 := createFundedAccount(t, 1000)
	account2 := createFundedAccount(t, 1000)

	arg := IdempotentTransferTxParams{
		TransferTxParams: TransferTxParams{
			FromAccountID: account1.ID,
			ToAccountID:   account2.ID,
			Amount:        100,
		},
		Owner:       account1.Owner,
		Key:         util.RandomString(20),
		RequestHash: util.RandomString(64),
	}

	first, err := store.IdempotentTransferTx(context.Background(), arg)
	require.NoError(t, err)
	require.Equal(t, int64(900), first.FromAccount.Balance)

	_, err = store.IdempotentTransferTx(context.Background(), arg)
	require.Error(t, err)
	var pqErr *pq.Error
	require.ErrorAs(t, err, &pqErr)
	require.Equal(t, "unique_violation", pqErr.Code.Name())

	// The money moved exactly once.
	after1, err := testQueries.GetAccount(context.Background(), account1.ID)
	require.NoError(t, err)
	require.Equal(t, int64(900), after1.Balance)

	after2, err := testQueries.GetAccount(context.Background(), account2.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1100), after2.Balance)
}

// The stored response has to be the real one, because a replay returns it verbatim
// instead of re-running the transfer.
func TestIdempotentTransferTxStoresTheResponse(t *testing.T) {
	store := NewStore(testDB)

	account1 := createFundedAccount(t, 500)
	account2 := createFundedAccount(t, 500)

	key := util.RandomString(20)
	result, err := store.IdempotentTransferTx(context.Background(), IdempotentTransferTxParams{
		TransferTxParams: TransferTxParams{
			FromAccountID: account1.ID,
			ToAccountID:   account2.ID,
			Amount:        50,
		},
		Owner:       account1.Owner,
		Key:         key,
		RequestHash: "fingerprint",
	})
	require.NoError(t, err)

	stored, err := testQueries.GetIdempotencyKey(context.Background(), GetIdempotencyKeyParams{
		Owner: account1.Owner,
		Key:   key,
	})
	require.NoError(t, err)
	require.Equal(t, "fingerprint", stored.RequestHash)
	require.NotEmpty(t, stored.ResponseBody)

	var replayed TransferTxResult
	require.NoError(t, json.Unmarshal([]byte(stored.ResponseBody), &replayed))
	require.Equal(t, result.Transfer.ID, replayed.Transfer.ID)
	require.Equal(t, result.FromAccount.Balance, replayed.FromAccount.Balance)
	require.Equal(t, result.ToAccount.Balance, replayed.ToAccount.Balance)
}

// A key is scoped to its owner, so two callers using the same string do not collide
// and cannot read each other's result.
func TestIdempotentTransferTxKeysAreScopedToOwner(t *testing.T) {
	store := NewStore(testDB)

	accountA := createFundedAccount(t, 500)
	accountB := createFundedAccount(t, 500)
	sharedKey := util.RandomString(20)

	_, err := store.IdempotentTransferTx(context.Background(), IdempotentTransferTxParams{
		TransferTxParams: TransferTxParams{FromAccountID: accountA.ID, ToAccountID: accountB.ID, Amount: 10},
		Owner:            accountA.Owner,
		Key:              sharedKey,
		RequestHash:      "a",
	})
	require.NoError(t, err)

	_, err = store.IdempotentTransferTx(context.Background(), IdempotentTransferTxParams{
		TransferTxParams: TransferTxParams{FromAccountID: accountB.ID, ToAccountID: accountA.ID, Amount: 10},
		Owner:            accountB.Owner,
		Key:              sharedKey,
		RequestHash:      "b",
	})
	require.NoError(t, err, "the same key under a different owner must not collide")
}

// A failed transfer must not burn the key: the claim rolls back with the transfer, so
// the caller can retry once the cause is fixed.
func TestIdempotentTransferTxFailureReleasesTheKey(t *testing.T) {
	store := NewStore(testDB)

	account1 := createFundedAccount(t, 100)
	account2 := createFundedAccount(t, 100)
	key := util.RandomString(20)

	// Overdraw, which the balance constraint rejects and which rolls the claim back.
	_, err := store.IdempotentTransferTx(context.Background(), IdempotentTransferTxParams{
		TransferTxParams: TransferTxParams{FromAccountID: account1.ID, ToAccountID: account2.ID, Amount: 1000},
		Owner:            account1.Owner,
		Key:              key,
		RequestHash:      "h",
	})
	require.Error(t, err)

	_, err = testQueries.GetIdempotencyKey(context.Background(), GetIdempotencyKeyParams{
		Owner: account1.Owner,
		Key:   key,
	})
	require.Error(t, err, "a rolled-back transfer must not leave the key claimed")

	// The same key now works for an affordable transfer.
	_, err = store.IdempotentTransferTx(context.Background(), IdempotentTransferTxParams{
		TransferTxParams: TransferTxParams{FromAccountID: account1.ID, ToAccountID: account2.ID, Amount: 10},
		Owner:            account1.Owner,
		Key:              key,
		RequestHash:      "h",
	})
	require.NoError(t, err)
}

// The real-world shape of the bug: a client whose request timed out fires the retry
// while the original is still in flight. Checking for the key and then transferring
// would let both through; claiming inside the transaction means the index serializes
// them and exactly one wins.
func TestIdempotentTransferTxConcurrentRetriesMoveMoneyOnce(t *testing.T) {
	store := NewStore(testDB)

	account1 := createFundedAccount(t, 1000)
	account2 := createFundedAccount(t, 1000)

	arg := IdempotentTransferTxParams{
		TransferTxParams: TransferTxParams{
			FromAccountID: account1.ID,
			ToAccountID:   account2.ID,
			Amount:        100,
		},
		Owner:       account1.Owner,
		Key:         util.RandomString(20),
		RequestHash: "h",
	}

	n := 8
	errs := make(chan error, n)
	for range make([]int, n) {
		go func() {
			_, err := store.IdempotentTransferTx(context.Background(), arg)
			errs <- err
		}()
	}

	succeeded := 0
	for range make([]int, n) {
		if err := <-errs; err == nil {
			succeeded++
		}
	}
	require.Equal(t, 1, succeeded, "exactly one of %d concurrent retries may succeed", n)

	after1, err := testQueries.GetAccount(context.Background(), account1.ID)
	require.NoError(t, err)
	require.Equal(t, int64(900), after1.Balance, "money must have moved exactly once")

	after2, err := testQueries.GetAccount(context.Background(), account2.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1100), after2.Balance)
}

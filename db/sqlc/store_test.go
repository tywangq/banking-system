package db

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestTransferTx(t *testing.T) {
	store := NewStore(testDB)

	account1 := createRandomAccount(t)
	account2 := createRandomAccount(t)
	fmt.Println(">> before:", account1.Balance, account2.Balance)

	// run n concurrent transfer transactions
	n := 5
	amount := int64(10)

	errs := make(chan error)
	results := make(chan TransferTxResult)

	for range make([]int, n) {
		// for i := range n {
		// txName := fmt.Sprintf("tx-%d", i+1)

		go func() {
			ctx := context.Background()
			// ctx := context.WithValue(context.Background(), txKey, txName)

			result, err := store.TransferTx(ctx, TransferTxParams{
				FromAccountID: account1.ID,
				ToAccountID:   account2.ID,
				Amount:        amount,
			})

			errs <- err
			results <- result
		}()
	}

	// check results
	existed := make(map[int]bool) // 模拟set，也可以用map[string]struct{}，existed[k] = struct{}{}，_, exists := existed[k]

	for range make([]int, n) {
		err := <-errs
		require.NoError(t, err)

		result := <-results
		require.NotEmpty(t, result)

		// check transfer
		transfer := result.Transfer
		require.NotEmpty(t, transfer)
		require.Equal(t, account1.ID, transfer.FromAccountID)
		require.Equal(t, account2.ID, transfer.ToAccountID)
		require.Equal(t, amount, transfer.Amount)
		require.NotZero(t, transfer.ID)
		require.NotZero(t, transfer.CreatedAt)
		_, err = store.GetTransfer(context.Background(), transfer.ID)
		require.NoError(t, err)

		// check entries
		fromEntry := result.FromEntry
		require.NotEmpty(t, fromEntry)
		require.Equal(t, account1.ID, fromEntry.AccountID)
		require.Equal(t, -amount, fromEntry.Amount)
		require.NotZero(t, fromEntry.ID)
		require.NotZero(t, fromEntry.CreatedAt)
		_, err = store.GetEntry(context.Background(), fromEntry.ID)
		require.NoError(t, err)

		toEntry := result.ToEntry
		require.NotEmpty(t, toEntry)
		require.Equal(t, account2.ID, toEntry.AccountID)
		require.Equal(t, amount, toEntry.Amount)
		require.NotZero(t, toEntry.ID)
		require.NotZero(t, toEntry.CreatedAt)
		_, err = store.GetEntry(context.Background(), toEntry.ID)
		require.NoError(t, err)

		// check accounts
		fromAccount := result.FromAccount
		require.NotEmpty(t, fromAccount)
		require.Equal(t, account1.ID, fromAccount.ID)

		toAccount := result.ToAccount
		require.NotEmpty(t, toAccount)
		require.Equal(t, account2.ID, toAccount.ID)

		fmt.Println(">> check:", fromAccount.Balance, toAccount.Balance)

		// check account intermediate balances
		diff1 := account1.Balance - fromAccount.Balance
		diff2 := toAccount.Balance - account2.Balance
		require.Equal(t, diff1, diff2)

		require.True(t, diff1 > 0)
		require.True(t, diff1%amount == 0)

		k := int(diff1 / amount)
		require.True(t, k >= 1 && k <= n)

		require.NotContains(t, existed, k)
		existed[k] = true
	}

	// check account final balances
	updatedAccount1, err := store.GetAccount(context.Background(), account1.ID)
	require.NoError(t, err)
	updatedAccount2, err := store.GetAccount(context.Background(), account2.ID)
	require.NoError(t, err)
	fmt.Println(">> after:", updatedAccount1.Balance, updatedAccount2.Balance)

	require.Equal(t, account1.Balance-int64(n)*amount, updatedAccount1.Balance)
	require.Equal(t, account2.Balance+int64(n)*amount, updatedAccount2.Balance)
}

func TestTransferTxDeadlock(t *testing.T) {
	store := NewStore(testDB)

	account1 := createRandomAccount(t)
	account2 := createRandomAccount(t)
	fmt.Println(">> before:", account1.Balance, account2.Balance)

	// run n concurrent transfer transactions
	n := 10
	amount := int64(10)

	errs := make(chan error)
	// results := make(chan TransferTxResult)

	for i := range make([]int, n) {
		// for i := range n {
		// txName := fmt.Sprintf("tx-%d", i+1)

		fromAccountID := account1.ID
		toAccountID := account2.ID
		if i%2 == 1 {
			fromAccountID = account2.ID
			toAccountID = account1.ID
		}

		go func() {
			ctx := context.Background()
			// ctx := context.WithValue(context.Background(), txKey, txName)

			_, err := store.TransferTx(ctx, TransferTxParams{
				FromAccountID: fromAccountID,
				ToAccountID:   toAccountID,
				Amount:        amount,
			})

			errs <- err
			// results <- result
		}()
	}

	// check results
	// existed := make(map[int]bool) // 模拟set，也可以用map[string]struct{}，existed[k] = struct{}{}，_, exists := existed[k]

	for range make([]int, n) {
		err := <-errs
		require.NoError(t, err)

		// 	result := <-results
		// 	require.NotEmpty(t, result)

		// 	// check transfer
		// 	transfer := result.Transfer
		// 	require.NotEmpty(t, transfer)
		// 	require.Equal(t, account1.ID, transfer.FromAccountID)
		// 	require.Equal(t, account2.ID, transfer.ToAccountID)
		// 	require.Equal(t, amount, transfer.Amount)
		// 	require.NotZero(t, transfer.ID)
		// 	require.NotZero(t, transfer.CreatedAt)
		// 	_, err = store.GetTransfer(context.Background(), transfer.ID)
		// 	require.NoError(t, err)

		// 	// check entries
		// 	fromEntry := result.FromEntry
		// 	require.NotEmpty(t, fromEntry)
		// 	require.Equal(t, account1.ID, fromEntry.AccountID)
		// 	require.Equal(t, -amount, fromEntry.Amount)
		// 	require.NotZero(t, fromEntry.ID)
		// 	require.NotZero(t, fromEntry.CreatedAt)
		// 	_, err = store.GetEntry(context.Background(), fromEntry.ID)
		// 	require.NoError(t, err)

		// 	toEntry := result.ToEntry
		// 	require.NotEmpty(t, toEntry)
		// 	require.Equal(t, account2.ID, toEntry.AccountID)
		// 	require.Equal(t, amount, toEntry.Amount)
		// 	require.NotZero(t, toEntry.ID)
		// 	require.NotZero(t, toEntry.CreatedAt)
		// 	_, err = store.GetEntry(context.Background(), toEntry.ID)
		// 	require.NoError(t, err)

		// 	// check accounts
		// 	fromAccount := result.FromAccount
		// 	require.NotEmpty(t, fromAccount)
		// 	require.Equal(t, account1.ID, fromAccount.ID)

		// 	toAccount := result.ToAccount
		// 	require.NotEmpty(t, toAccount)
		// 	require.Equal(t, account2.ID, toAccount.ID)

		// 	fmt.Println(">> check:", fromAccount.Balance, toAccount.Balance)

		// 	// check account intermediate balances
		// 	diff1 := account1.Balance - fromAccount.Balance
		// 	diff2 := toAccount.Balance - account2.Balance
		// 	require.Equal(t, diff1, diff2)

		// 	require.True(t, diff1 > 0)
		// 	require.True(t, diff1%amount == 0)

		// 	k := int(diff1 / amount)
		// 	require.True(t, k >= 1 && k <= n)

		// 	require.NotContains(t, existed, k)
		// 	existed[k] = true
	}

	// // check account final balances
	updatedAccount1, err := store.GetAccount(context.Background(), account1.ID)
	require.NoError(t, err)
	updatedAccount2, err := store.GetAccount(context.Background(), account2.ID)
	require.NoError(t, err)
	fmt.Println(">> after:", updatedAccount1.Balance, updatedAccount2.Balance)

	require.Equal(t, account1.Balance, updatedAccount1.Balance)
	require.Equal(t, account2.Balance, updatedAccount2.Balance)
}

// TestTransferTxRejectsOverdraft proves the constraint itself, not the handler's
// error mapping: a single transfer larger than the balance must be refused by
// Postgres, and the rollback must leave both balances untouched.
func TestTransferTxRejectsOverdraft(t *testing.T) {
	store := NewStore(testDB)

	account1 := createRandomAccount(t)
	account2 := createRandomAccount(t)

	_, err := store.TransferTx(context.Background(), TransferTxParams{
		FromAccountID: account1.ID,
		ToAccountID:   account2.ID,
		Amount:        account1.Balance + 1,
	})
	require.Error(t, err)

	var pqErr *pq.Error
	require.True(t, errors.As(err, &pqErr), "expected a pq error, got %T: %v", err, err)
	require.Equal(t, "accounts_balance_non_negative", pqErr.Constraint)

	// The whole transaction has to roll back -- the transfer row and the ledger
	// entries must not survive a rejected transfer either.
	after1, err := testQueries.GetAccount(context.Background(), account1.ID)
	require.NoError(t, err)
	require.Equal(t, account1.Balance, after1.Balance)

	after2, err := testQueries.GetAccount(context.Background(), account2.ID)
	require.NoError(t, err)
	require.Equal(t, account2.Balance, after2.Balance)
}

// TestTransferTxConcurrentOverdraftLeavesBalanceNonNegative is the case an
// application-level balance check cannot handle: every transfer is individually
// affordable, but together they exceed the balance. Some must fail, and the
// balance must never end up below zero.
func TestTransferTxConcurrentOverdraftLeavesBalanceNonNegative(t *testing.T) {
	store := NewStore(testDB)

	account1 := createRandomAccount(t)
	account2 := createRandomAccount(t)

	// Fund a known balance so the arithmetic is exact.
	funded, err := testQueries.AddAccountBalance(context.Background(), AddAccountBalanceParams{
		ID:     account1.ID,
		Amount: 100 - account1.Balance,
	})
	require.NoError(t, err)
	require.Equal(t, int64(100), funded.Balance)

	// 20 concurrent transfers of 10 against a balance of 100: at most 10 can win.
	n := 20
	amount := int64(10)
	errs := make(chan error, n)

	for range make([]int, n) {
		go func() {
			_, err := store.TransferTx(context.Background(), TransferTxParams{
				FromAccountID: account1.ID,
				ToAccountID:   account2.ID,
				Amount:        amount,
			})
			errs <- err
		}()
	}

	succeeded := 0
	for range make([]int, n) {
		if err := <-errs; err == nil {
			succeeded++
		}
	}

	require.Equal(t, 10, succeeded, "exactly the affordable transfers should win")

	after1, err := testQueries.GetAccount(context.Background(), account1.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), after1.Balance)
	require.GreaterOrEqual(t, after1.Balance, int64(0))

	after2, err := testQueries.GetAccount(context.Background(), account2.ID)
	require.NoError(t, err)
	require.Equal(t, account2.Balance+int64(succeeded)*amount, after2.Balance)
}

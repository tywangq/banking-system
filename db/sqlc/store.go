package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

type Store interface {
	Querier
	TransferTx(ctx context.Context, arg TransferTxParams) (TransferTxResult, error)
	IdempotentTransferTx(ctx context.Context, arg IdempotentTransferTxParams) (TransferTxResult, error)
}

type SQLStore struct {
	*Queries         // 用于执行查询；组合 -> 直接store.GetUser()，无需store.Queries.GetUser()
	db       *sql.DB // 用于事务管理
}

func NewStore(db *sql.DB) Store {
	return &SQLStore{
		Queries: New(db), // 匿名字段会被自动赋名为类型名（去掉前面的*或者其他修饰符）
		db:      db,
	}
}

func (store *SQLStore) execTx(ctx context.Context, fn func(*Queries) error) error { // 对外不可见
	tx, err := store.db.BeginTx(ctx, nil) // 1
	if err != nil {
		return err
	}

	q := New(tx)                  // 2
	if err := fn(q); err != nil { // 3
		if rbErr := tx.Rollback(); rbErr != nil { // 4
			return fmt.Errorf("tx err: %v, rb err: %v", err, rbErr)
		}
		return err
	}

	return tx.Commit() // 5
}

type TransferTxParams struct {
	FromAccountID int64 `json:"from_account_id"`
	ToAccountID   int64 `json:"to_account_id"`
	Amount        int64 `json:"amount"`
}

type TransferTxResult struct {
	Transfer    Transfer `json:"transfer"`
	FromEntry   Entry    `json:"from_entry"`
	ToEntry     Entry    `json:"to_entry"`
	FromAccount Account  `json:"from_account"`
	ToAccount   Account  `json:"to_account"`
}

// var txKey = struct{}{}

// all in one: transfer, entries*2, accounts*2
func (store *SQLStore) TransferTx(ctx context.Context, arg TransferTxParams) (TransferTxResult, error) {
	var result TransferTxResult

	err := store.execTx(ctx, func(q *Queries) error {
		var err error
		result, err = transferTx(ctx, q, arg)
		return err
	})

	return result, err
}

// transferTx holds the actual transfer work so that TransferTx and
// IdempotentTransferTx can run it inside their own transaction. It takes a *Queries
// rather than opening one, which is the whole point: the caller decides what else
// belongs in the same transaction.
func transferTx(ctx context.Context, q *Queries, arg TransferTxParams) (TransferTxResult, error) {
	var result TransferTxResult
	var err error

	result.Transfer, err = q.CreateTransfer(ctx, CreateTransferParams(arg))
	if err != nil {
		return result, err
	}

	result.FromEntry, err = q.CreateEntry(ctx, CreateEntryParams{
		AccountID: arg.FromAccountID,
		Amount:    -arg.Amount,
	})
	if err != nil {
		return result, err
	}

	result.ToEntry, err = q.CreateEntry(ctx, CreateEntryParams{
		AccountID: arg.ToAccountID,
		Amount:    arg.Amount,
	})
	if err != nil {
		return result, err
	}

	// Always touch the lower account ID first. Two transfers moving money in
	// opposite directions between the same pair then take their locks in the same
	// order, so one waits instead of both deadlocking. TestTransferTxDeadlock covers
	// it. (Earlier attempts kept in git history: GetAccountForUpdate + UpdateAccount,
	// and AddAccountBalance without ordering -- the latter is what deadlocks.)
	if arg.FromAccountID < arg.ToAccountID {
		result.FromAccount, result.ToAccount, err = addMoney(ctx, q, arg.FromAccountID, -arg.Amount, arg.ToAccountID, arg.Amount)
	} else {
		result.ToAccount, result.FromAccount, err = addMoney(ctx, q, arg.ToAccountID, arg.Amount, arg.FromAccountID, -arg.Amount)
	}

	return result, err
}

type IdempotentTransferTxParams struct {
	TransferTxParams
	Owner       string
	Key         string
	RequestHash string
}

// IdempotentTransferTx claims the idempotency key and performs the transfer in one
// transaction, claim first.
//
// The ordering is the entire design. Checking "has this key been used?" and then
// transferring would be the same race as checking a balance before debiting it: two
// concurrent retries both see no key and both move the money. Inserting the claim
// first hands the mutual exclusion to the primary key on (owner, key) -- the second
// request blocks on the index until the first commits, then fails to insert, and the
// caller turns that failure into a replay of the stored response.
//
// A failed transfer rolls the claim back with it, so the key stays available. That is
// deliberate: the key exists to stop a *successful* transfer from happening twice, not
// to make a failure permanent.
func (store *SQLStore) IdempotentTransferTx(ctx context.Context, arg IdempotentTransferTxParams) (TransferTxResult, error) {
	var result TransferTxResult

	err := store.execTx(ctx, func(q *Queries) error {
		if _, err := q.CreateIdempotencyKey(ctx, CreateIdempotencyKeyParams{
			Owner:       arg.Owner,
			Key:         arg.Key,
			RequestHash: arg.RequestHash,
		}); err != nil {
			return err
		}

		var err error
		result, err = transferTx(ctx, q, arg.TransferTxParams)
		if err != nil {
			return err
		}

		body, err := json.Marshal(result)
		if err != nil {
			return err
		}

		_, err = q.CompleteIdempotencyKey(ctx, CompleteIdempotencyKeyParams{
			Owner:        arg.Owner,
			Key:          arg.Key,
			ResponseBody: string(body),
		})
		return err
	})

	return result, err
}

func addMoney(ctx context.Context, q *Queries, accountID1 int64, amount1 int64, accountID2 int64, amount2 int64) (account1 Account, account2 Account, err error) {
	account1, err = q.AddAccountBalance(ctx, AddAccountBalanceParams{
		ID:     accountID1,
		Amount: amount1,
	})
	if err != nil {
		return
	}
	account2, err = q.AddAccountBalance(ctx, AddAccountBalanceParams{
		ID:     accountID2,
		Amount: amount2,
	})
	return
}

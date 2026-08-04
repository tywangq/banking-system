package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	db "github.com/tywangq/banking-system/db/sqlc"
	"github.com/tywangq/banking-system/token"
)

type transferRequest struct {
	FromAccountID int64  `json:"from_account_id" binding:"required,min=1"`
	ToAccountID   int64  `json:"to_account_id" binding:"required,min=1"`
	Amount        int64  `json:"amount" binding:"required,gt=0"`
	Currency      string `json:"currency" binding:"required,currency"`
}

func (server *Server) createTransfer(ctx *gin.Context) {
	var req transferRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	if req.FromAccountID == req.ToAccountID {
		err := fmt.Errorf("from_account_id %d is the same as to_account_id %d", req.FromAccountID, req.ToAccountID)
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	fromAccount, valid := server.validAccount(ctx, req.FromAccountID, req.Currency)
	if !valid {
		return
	}

	authPayload := ctx.MustGet(authorizationPayloadKey).(*token.Payload)
	if fromAccount.Owner != authPayload.Username {
		err := fmt.Errorf("from_account %d doesn't belong to the authenticated user %s", req.FromAccountID, authPayload.Username)
		ctx.JSON(http.StatusUnauthorized, errorResponse(err))
		return
	}

	_, valid = server.validAccount(ctx, req.ToAccountID, req.Currency)
	if !valid {
		return
	}

	arg := db.TransferTxParams{
		FromAccountID: req.FromAccountID,
		ToAccountID:   req.ToAccountID,
		Amount:        req.Amount,
	}

	// An Idempotency-Key opts the caller into replay protection: a retry of a request
	// whose response was never seen returns the original result instead of moving the
	// money a second time. It is optional so existing callers keep working, but a
	// caller that omits it gets no protection -- in a greenfield API this would be
	// required on this endpoint.
	if key := ctx.GetHeader(idempotencyKeyHeader); key != "" {
		server.createTransferIdempotent(ctx, req, arg, authPayload.Username, key)
		return
	}

	result, err := server.store.TransferTx(ctx, arg)
	if err != nil {
		// The accounts_balance_non_negative constraint is what actually prevents an
		// overdraft. Checking the balance here before calling TransferTx would be
		// racy: two concurrent transfers can both read a sufficient balance and both
		// proceed. Postgres serializes them and rejects the one that would go
		// negative, which is a client error rather than a server error.
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Constraint == "accounts_balance_non_negative" {
			err = fmt.Errorf("insufficient balance in account %d", req.FromAccountID)
			ctx.JSON(http.StatusBadRequest, errorResponse(err))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	ctx.JSON(http.StatusOK, result)
}

func (server *Server) validAccount(ctx *gin.Context, accountID int64, currency string) (db.Account, bool) {
	account, err := server.store.GetAccount(ctx, accountID)
	if err != nil {
		if err == sql.ErrNoRows {
			ctx.JSON(http.StatusNotFound, errorResponse(err))
			return account, false
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return account, false
	}

	if account.Currency != currency {
		err := fmt.Errorf("account %d currency mismatch: %s vs %s", account.ID, account.Currency, currency)
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return account, false
	}

	return account, true
}

const (
	idempotencyKeyHeader = "Idempotency-Key"
	maxIdempotencyKeyLen = 128
)

// requestFingerprint identifies the request a key was spent on, so the same key
// arriving with different parameters can be rejected instead of being answered with a
// stored response to a different question.
func requestFingerprint(req transferRequest) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%s",
		req.FromAccountID, req.ToAccountID, req.Amount, req.Currency)))
	return hex.EncodeToString(sum[:])
}

func (server *Server) createTransferIdempotent(
	ctx *gin.Context,
	req transferRequest,
	arg db.TransferTxParams,
	owner string,
	key string,
) {
	if len(key) > maxIdempotencyKeyLen {
		err := fmt.Errorf("%s must be at most %d characters", idempotencyKeyHeader, maxIdempotencyKeyLen)
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	fingerprint := requestFingerprint(req)

	result, err := server.store.IdempotentTransferTx(ctx, db.IdempotentTransferTxParams{
		TransferTxParams: arg,
		Owner:            owner,
		Key:              key,
		RequestHash:      fingerprint,
	})
	if err == nil {
		ctx.JSON(http.StatusOK, result)
		return
	}

	if pqErr, ok := err.(*pq.Error); ok {
		switch {
		// The key was already spent. Because the claim is inserted inside the same
		// transaction as the transfer, reaching here means the first request has
		// committed -- so its stored response is readable now.
		case pqErr.Code.Name() == "unique_violation":
			server.replayIdempotentTransfer(ctx, owner, key, fingerprint)
			return
		case pqErr.Constraint == "accounts_balance_non_negative":
			err = fmt.Errorf("insufficient balance in account %d", req.FromAccountID)
			ctx.JSON(http.StatusBadRequest, errorResponse(err))
			return
		}
	}

	ctx.JSON(http.StatusInternalServerError, errorResponse(err))
}

func (server *Server) replayIdempotentTransfer(ctx *gin.Context, owner, key, fingerprint string) {
	stored, err := server.store.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{
		Owner: owner,
		Key:   key,
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	// Same key, different request: a client bug. Replaying the stored response would
	// answer a question that was never asked, so refuse instead.
	if stored.RequestHash != fingerprint {
		err := fmt.Errorf("%s was already used for a different transfer", idempotencyKeyHeader)
		ctx.JSON(http.StatusConflict, errorResponse(err))
		return
	}

	// Claim and completion share one transaction, so a committed row always carries
	// its response. An empty body would mean that invariant broke; report a conflict
	// rather than replaying nothing.
	if stored.ResponseBody == "" {
		err := fmt.Errorf("%s is still being processed", idempotencyKeyHeader)
		ctx.JSON(http.StatusConflict, errorResponse(err))
		return
	}

	ctx.Header("Idempotent-Replay", "true")
	ctx.Data(http.StatusOK, "application/json; charset=utf-8", []byte(stored.ResponseBody))
}

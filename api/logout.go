package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/tywangq/banking-system/token"
)

// The sessions table has always carried is_blocked, and renewAccessToken has always
// refused a blocked session -- but nothing ever set the flag, so there was no way to
// sign out. These two handlers are the missing write side.
//
// One limitation worth being explicit about: revoking a session stops refresh, not the
// access token already in the caller's hands. Access tokens are stateless, so
// invalidating one would need a denylist consulted on every request, which gives up
// the property that makes stateless tokens worth having. The exposure is bounded by
// ACCESS_TOKEN_DURATION instead, which is 15 minutes.

type logoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type logoutResponse struct {
	SessionID string `json:"session_id"`
}

// logout revokes the single session identified by the supplied refresh token, which is
// how a caller signs out one device without touching the others.
func (server *Server) logout(ctx *gin.Context) {
	var req logoutRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	refreshPayload, err := server.tokenMaker.VerifyToken(req.RefreshToken)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, errorResponse(err))
		return
	}

	session, err := server.store.GetSession(ctx, refreshPayload.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			ctx.JSON(http.StatusNotFound, errorResponse(err))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	// Without this check, anyone holding a valid access token could revoke any
	// session whose refresh token they got hold of, including another user's.
	authPayload := ctx.MustGet(authorizationPayloadKey).(*token.Payload)
	if session.Username != authPayload.Username {
		err := fmt.Errorf("session doesn't belong to the authenticated user")
		ctx.JSON(http.StatusUnauthorized, errorResponse(err))
		return
	}

	// Already revoked: report success rather than an error. Signing out twice is not
	// a failure, and a caller retrying after a dropped response should not see one.
	if session.IsBlocked {
		ctx.JSON(http.StatusOK, logoutResponse{SessionID: session.ID.String()})
		return
	}

	blocked, err := server.store.BlockSession(ctx, session.ID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	ctx.JSON(http.StatusOK, logoutResponse{SessionID: blocked.ID.String()})
}

type logoutAllResponse struct {
	SessionsRevoked int64 `json:"sessions_revoked"`
}

// logoutAll revokes every open session for the authenticated user. This is the
// "I think someone else has my password" path, so it deliberately takes no session
// identifier -- the caller should not need to know what else is signed in.
func (server *Server) logoutAll(ctx *gin.Context) {
	authPayload := ctx.MustGet(authorizationPayloadKey).(*token.Payload)

	revoked, err := server.store.BlockUserSessions(ctx, authPayload.Username)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	ctx.JSON(http.StatusOK, logoutAllResponse{SessionsRevoked: revoked})
}

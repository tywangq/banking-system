package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tywangq/banking-system/util"
)

func createRandomSession(t *testing.T, username string) Session {
	arg := CreateSessionParams{
		ID:           uuid.New(),
		Username:     username,
		RefreshToken: util.RandomString(32),
		UserAgent:    "test-agent",
		ClientIp:     "127.0.0.1",
		IsBlocked:    false,
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}

	session, err := testQueries.CreateSession(context.Background(), arg)
	require.NoError(t, err)
	require.False(t, session.IsBlocked)

	return session
}

// Blocking rather than deleting is deliberate: the user agent and client IP of a
// signed-out session stay inspectable afterwards.
func TestBlockSessionKeepsTheRow(t *testing.T) {
	user := createRandomUser(t)
	session := createRandomSession(t, user.Username)

	blocked, err := testQueries.BlockSession(context.Background(), session.ID)
	require.NoError(t, err)
	require.True(t, blocked.IsBlocked)
	require.Equal(t, session.ID, blocked.ID)
	require.Equal(t, session.UserAgent, blocked.UserAgent)
	require.Equal(t, session.ClientIp, blocked.ClientIp)

	fetched, err := testQueries.GetSession(context.Background(), session.ID)
	require.NoError(t, err)
	require.True(t, fetched.IsBlocked)
}

func TestBlockSessionOnlyAffectsThatSession(t *testing.T) {
	user := createRandomUser(t)
	keep := createRandomSession(t, user.Username)
	revoke := createRandomSession(t, user.Username)

	_, err := testQueries.BlockSession(context.Background(), revoke.ID)
	require.NoError(t, err)

	untouched, err := testQueries.GetSession(context.Background(), keep.ID)
	require.NoError(t, err)
	require.False(t, untouched.IsBlocked, "signing out one device must leave the others alone")
}

// Sign-out-everywhere must cover the caller's sessions and nobody else's, and must
// report how many were actually open.
func TestBlockUserSessions(t *testing.T) {
	user := createRandomUser(t)
	bystander := createRandomUser(t)

	for range make([]int, 3) {
		createRandomSession(t, user.Username)
	}
	othersSession := createRandomSession(t, bystander.Username)

	revoked, err := testQueries.BlockUserSessions(context.Background(), user.Username)
	require.NoError(t, err)
	require.Equal(t, int64(3), revoked)

	stillOpen, err := testQueries.GetSession(context.Background(), othersSession.ID)
	require.NoError(t, err)
	require.False(t, stillOpen.IsBlocked, "another user's session must not be revoked")

	// Already-blocked sessions are excluded, so a second call reports zero rather
	// than re-counting rows it did not change.
	again, err := testQueries.BlockUserSessions(context.Background(), user.Username)
	require.NoError(t, err)
	require.Equal(t, int64(0), again)
}

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrRefreshTokenNotFound means the token names no live record — already used,
// logged out, or simply expired. Consume and the not-found case of Delete both
// answer this; the caller cannot and need not tell the three apart.
var ErrRefreshTokenNotFound = errors.New("refresh token not found or expired")

// refreshKeyPrefix and refreshUserSetPrefix key the two shapes this repository
// stores: one row per token, and a per-user index of that user's live tokens.
const (
	refreshKeyPrefix     = "refresh:"
	refreshUserSetPrefix = "refresh:user:"
)

// RefreshRecord is what a refresh token remembers about the session it was
// issued for — just enough to reissue an access token on Refresh without
// storing the access token itself anywhere. IDUserRole is nil when the
// session it came from had no active context yet (isu #12 fase 4's shape),
// mirroring model.ActiveContext being nil.
type RefreshRecord struct {
	UserID     int64  `json:"user_id"`
	IDUserRole *int64 `json:"id_user_role"`
}

// RefreshTokenRepository owns every Redis key a refresh token touches. It is
// deliberately not named alongside the SQL repositories' DBTX shape: Redis is
// the second storage target the layered architecture already names
// ("PostgreSQL / Redis / upstream HTTP"), so this is that seam for auth.
type RefreshTokenRepository struct {
	Redis *redis.Client
}

func NewRefreshTokenRepository(client *redis.Client) *RefreshTokenRepository {
	return &RefreshTokenRepository{Redis: client}
}

func refreshKey(token string) string { return refreshKeyPrefix + token }

func refreshUserSetKey(userID int64) string { return fmt.Sprintf("%s%d", refreshUserSetPrefix, userID) }

// Store writes a new refresh token record and indexes it under its owner.
//
// The user-set is re-EXPIRE'd to ttl on every Store, not just created once:
// that is what keeps an abandoned session's index entry from outliving every
// token it ever names. A user who never logs in again for a full refresh TTL
// loses the index key on its own; one who keeps rotating keeps it alive.
func (r *RefreshTokenRepository) Store(ctx context.Context, token string, record RefreshRecord, ttl time.Duration) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal refresh record: %w", err)
	}

	if err := r.Redis.Set(ctx, refreshKey(token), payload, ttl).Err(); err != nil {
		return fmt.Errorf("store refresh token: %w", err)
	}

	setKey := refreshUserSetKey(record.UserID)

	if err := r.Redis.SAdd(ctx, setKey, token).Err(); err != nil {
		return fmt.Errorf("index refresh token: %w", err)
	}

	if err := r.Redis.Expire(ctx, setKey, ttl).Err(); err != nil {
		return fmt.Errorf("extend refresh index ttl: %w", err)
	}

	return nil
}

// Consume atomically reads and deletes a refresh token — GETDEL closes the
// reuse race by construction: a token can be popped at most once, so a second
// attempt with the same value always meets ErrRefreshTokenNotFound, whether
// the first attempt was legitimate rotation or theft-and-replay.
func (r *RefreshTokenRepository) Consume(ctx context.Context, token string) (*RefreshRecord, error) {
	payload, err := r.Redis.GetDel(ctx, refreshKey(token)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrRefreshTokenNotFound
		}

		return nil, fmt.Errorf("consume refresh token: %w", err)
	}

	var record RefreshRecord
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		return nil, fmt.Errorf("unmarshal refresh record: %w", err)
	}

	// Best-effort: the primary key is already gone, which is what actually
	// stops reuse. A stale member left in the index only costs
	// RevokeAllForUser one harmless DEL of an already-absent key.
	_ = r.Redis.SRem(ctx, refreshUserSetKey(record.UserID), token).Err()

	return &record, nil
}

// Delete revokes one token — logout. It is Consume with the not-found case
// swallowed: logging out an already-used or already-expired token is not an
// error, it is the state logout was trying to reach anyway.
func (r *RefreshTokenRepository) Delete(ctx context.Context, token string) error {
	_, err := r.Consume(ctx, token)
	if err != nil && !errors.Is(err, ErrRefreshTokenNotFound) {
		return err
	}

	return nil
}

// RevokeAllForUser deletes every refresh token a user currently holds — the
// mechanism behind isu #24 fase 3's revocation triggers (password change,
// is_aktif = false, every grant revoked). It reads the index once and
// pipelines the deletes, rather than one round trip per token.
func (r *RefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID int64) error {
	setKey := refreshUserSetKey(userID)

	tokens, err := r.Redis.SMembers(ctx, setKey).Result()
	if err != nil {
		return fmt.Errorf("list refresh tokens for user: %w", err)
	}

	if len(tokens) == 0 {
		// Still delete the set key itself: it may exist with zero members if
		// every member was already reaped by Consume without the set ever
		// being cleaned up.
		if err := r.Redis.Del(ctx, setKey).Err(); err != nil {
			return fmt.Errorf("delete empty refresh index: %w", err)
		}

		return nil
	}

	pipe := r.Redis.Pipeline()
	for _, token := range tokens {
		pipe.Del(ctx, refreshKey(token))
	}
	pipe.Del(ctx, setKey)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("revoke refresh tokens: %w", err)
	}

	return nil
}

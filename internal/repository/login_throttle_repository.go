package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// LoginThrottleRepository is a thin Redis counter with no domain knowledge of
// what it is counting — the caller (AuthUseCase) builds the key from
// (ip, username) and owns the threshold. Keeping this a dumb primitive is
// what lets the usecase layer own the policy, the same split every SQL
// repository already keeps from its usecase.
type LoginThrottleRepository struct {
	Redis *redis.Client
}

func NewLoginThrottleRepository(client *redis.Client) *LoginThrottleRepository {
	return &LoginThrottleRepository{Redis: client}
}

// Peek reads the current count without changing it — 0 for a key that does
// not exist, the same "absent means zero" reading kartu_stok's balance read
// uses.
func (r *LoginThrottleRepository) Peek(ctx context.Context, key string) (int64, error) {
	count, err := r.Redis.Get(ctx, key).Int64()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, nil
		}

		return 0, fmt.Errorf("peek login throttle: %w", err)
	}

	return count, nil
}

// Increment counts one more failure and returns the new total. The window
// is only (re)armed the instant the key is created (result == 1) — an
// INCR on every call would keep pushing the expiry out and the window would
// never actually close as long as failures kept arriving, which is a
// permanent lockout dressed up as a sliding one.
func (r *LoginThrottleRepository) Increment(ctx context.Context, key string, window time.Duration) (int64, error) {
	count, err := r.Redis.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("increment login throttle: %w", err)
	}

	if count == 1 {
		if err := r.Redis.Expire(ctx, key, window).Err(); err != nil {
			return 0, fmt.Errorf("arm login throttle window: %w", err)
		}
	}

	return count, nil
}

// Reset clears the counter — called on a successful login so a legitimate
// caller who eventually gets the password right is not still paying for
// earlier mistakes.
func (r *LoginThrottleRepository) Reset(ctx context.Context, key string) error {
	if err := r.Redis.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("reset login throttle: %w", err)
	}

	return nil
}

package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

func (r *UserRepo) GetPasswordHash(ctx context.Context, q Querier, username string) (string, error) {
	var hash string
	err := q.QueryRow(ctx, `SELECT password_hash FROM users WHERE username=$1`, username).Scan(&hash)
	return hash, err
}

func (r *UserRepo) Create(ctx context.Context, q Querier, username, hash string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2)`,
		username, hash)
	return err
}

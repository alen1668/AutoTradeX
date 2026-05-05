package store

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type SignalRow struct {
	ID             int64
	StrategyID     string
	Symbol         string
	Kind           string
	SignalPrice    decimal.Decimal
	TVTimestampMs  int64
	ReceivedAt     time.Time
	RawPayload     json.RawMessage
	ClientIP       net.IP
	Decision       string
	DecisionReason string
	TraceID        string
}

type SignalRepo struct {
	pool *pgxpool.Pool
}

func NewSignalRepo(pool *pgxpool.Pool) *SignalRepo { return &SignalRepo{pool: pool} }

// Insert inserts a row. Returns (id, duplicate, error). On duplicate it returns
// the EXISTING row's id; the caller should treat duplicate=true as "already
// processed, skip work".
func (r *SignalRepo) Insert(ctx context.Context, q Querier, in SignalRow) (int64, bool, error) {
	const sql = `
INSERT INTO signals
  (strategy_id, symbol, kind, signal_price, tv_timestamp_ms, received_at,
   raw_payload, client_ip, decision, decision_reason, trace_id)
VALUES ($1,$2,$3::signal_kind,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (strategy_id, tv_timestamp_ms) DO NOTHING
RETURNING id`
	var id int64
	err := q.QueryRow(ctx, sql,
		in.StrategyID, in.Symbol, in.Kind, in.SignalPrice, in.TVTimestampMs, in.ReceivedAt,
		in.RawPayload, ipOrNil(in.ClientIP), in.Decision, nullableString(in.DecisionReason), in.TraceID,
	).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// Duplicate — fetch existing id
		var existing int64
		if err2 := q.QueryRow(ctx,
			`SELECT id FROM signals WHERE strategy_id=$1 AND tv_timestamp_ms=$2`,
			in.StrategyID, in.TVTimestampMs,
		).Scan(&existing); err2 != nil {
			return 0, false, err2
		}
		return existing, true, nil
	}
	return 0, false, err
}

func (r *SignalRepo) UpdateDecision(ctx context.Context, q Querier, id int64, decision, reason string) error {
	_, err := q.Exec(ctx,
		`UPDATE signals SET decision=$1, decision_reason=$2 WHERE id=$3`,
		decision, nullableString(reason), id)
	return err
}

func (r *SignalRepo) GetByID(ctx context.Context, q Querier, id int64) (*SignalRow, error) {
	var s SignalRow
	var rawPayload []byte
	var clientIP *net.IP
	var decisionReason *string
	err := q.QueryRow(ctx,
		`SELECT id, strategy_id, symbol, kind::text, signal_price, tv_timestamp_ms,
                received_at, raw_payload, client_ip, decision, decision_reason, trace_id
           FROM signals WHERE id=$1`, id,
	).Scan(&s.ID, &s.StrategyID, &s.Symbol, &s.Kind, &s.SignalPrice, &s.TVTimestampMs,
		&s.ReceivedAt, &rawPayload, &clientIP, &s.Decision, &decisionReason, &s.TraceID)
	if err != nil {
		return nil, err
	}
	s.RawPayload = json.RawMessage(rawPayload)
	if clientIP != nil {
		s.ClientIP = *clientIP
	}
	if decisionReason != nil {
		s.DecisionReason = *decisionReason
	}
	return &s, nil
}

func (r *SignalRepo) ListRecent(ctx context.Context, q Querier, limit int) ([]*SignalRow, error) {
	rows, err := q.Query(ctx,
		`SELECT id, strategy_id, symbol, kind::text, signal_price, tv_timestamp_ms,
                received_at, raw_payload, client_ip, decision, decision_reason, trace_id
           FROM signals
          ORDER BY received_at DESC
          LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SignalRow{}
	for rows.Next() {
		var s SignalRow
		var rawPayload []byte
		var clientIP *net.IP
		var decisionReason *string
		if err := rows.Scan(&s.ID, &s.StrategyID, &s.Symbol, &s.Kind, &s.SignalPrice, &s.TVTimestampMs,
			&s.ReceivedAt, &rawPayload, &clientIP, &s.Decision, &decisionReason, &s.TraceID); err != nil {
			return nil, err
		}
		s.RawPayload = json.RawMessage(rawPayload)
		if clientIP != nil {
			s.ClientIP = *clientIP
		}
		if decisionReason != nil {
			s.DecisionReason = *decisionReason
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

func ipOrNil(ip net.IP) any {
	if ip == nil {
		return nil
	}
	return ip.String()
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

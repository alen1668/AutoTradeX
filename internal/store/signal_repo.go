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

// SignalFilter narrows ListPage results. Empty fields disable that filter.
type SignalFilter struct {
	Decision   string // "accepted"|"duplicate"|"risk_denied"|"disarmed"|"invalid"|""
	StrategyID string
	Symbol     string
}

// ListPage returns one page of signals (newest first) matching filter, plus
// the total matching count for pagination.
func (r *SignalRepo) ListPage(ctx context.Context, q Querier, f SignalFilter, limit, offset int) ([]*SignalRow, int, error) {
	where, args := buildSignalsWhere(f)
	countSQL := `SELECT COUNT(*) FROM signals` + where
	var total int
	if err := q.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	listSQL := `SELECT id, strategy_id, symbol, kind::text, signal_price, tv_timestamp_ms,
                received_at, raw_payload, client_ip, decision, decision_reason, trace_id
           FROM signals` + where + `
          ORDER BY received_at DESC
          LIMIT $` + itoa(len(args)-1) + ` OFFSET $` + itoa(len(args))
	rows, err := q.Query(ctx, listSQL, args...)
	if err != nil {
		return nil, 0, err
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
			return nil, 0, err
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
	return out, total, rows.Err()
}

// buildSignalsWhere builds a WHERE clause + positional args from filter.
func buildSignalsWhere(f SignalFilter) (string, []any) {
	var clauses []string
	var args []any
	if f.Decision != "" {
		args = append(args, f.Decision)
		clauses = append(clauses, "decision = $"+itoa(len(args)))
	}
	if f.StrategyID != "" {
		args = append(args, f.StrategyID)
		clauses = append(clauses, "strategy_id = $"+itoa(len(args)))
	}
	if f.Symbol != "" {
		args = append(args, f.Symbol)
		clauses = append(clauses, "symbol = $"+itoa(len(args)))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + joinAnd(clauses), args
}

// DistinctStrategies / DistinctSymbols power the filter dropdowns. Cheap on
// small tables; if signals grows huge we can swap to a materialized list.
func (r *SignalRepo) DistinctStrategies(ctx context.Context, q Querier) ([]string, error) {
	return scanStringList(ctx, q, `SELECT DISTINCT strategy_id FROM signals ORDER BY 1`)
}

func (r *SignalRepo) DistinctSymbols(ctx context.Context, q Querier) ([]string, error) {
	return scanStringList(ctx, q, `SELECT DISTINCT symbol FROM signals ORDER BY 1`)
}

func scanStringList(ctx context.Context, q Querier, sql string) ([]string, error) {
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *SignalRepo) ExistsByKey(ctx context.Context, q Querier, strategyID string, tvTimestampMs int64) (bool, error) {
	var n int
	err := q.QueryRow(ctx,
		`SELECT 1 FROM signals WHERE strategy_id=$1 AND tv_timestamp_ms=$2 LIMIT 1`,
		strategyID, tvTimestampMs,
	).Scan(&n)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, err
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

func itoa(n int) string {
	// Tiny inline strconv.Itoa to avoid the import in this file.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func joinAnd(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += " AND " + parts[i]
	}
	return out
}

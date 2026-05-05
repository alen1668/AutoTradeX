-- +goose Up
-- +goose StatementBegin

-- ========== 枚举类型 ==========
CREATE TYPE signal_kind AS ENUM ('long', 'short', 'exit_long', 'exit_short');
COMMENT ON TYPE signal_kind IS '信号方向：开多/开空/平多/平空';

CREATE TYPE order_status AS ENUM ('pending','submitted','partial','filled','canceled','rejected','expired');
COMMENT ON TYPE order_status IS '订单状态';

CREATE TYPE bot_mode AS ENUM ('dry_run','testnet','live');
COMMENT ON TYPE bot_mode IS '运行模式';

-- ========== strategies ==========
CREATE TABLE strategies (
    id              TEXT PRIMARY KEY,
    symbol          TEXT NOT NULL,
    leverage        SMALLINT NOT NULL,
    size_usdc       NUMERIC(20,8) NOT NULL,
    stop_loss_pct   NUMERIC(6,3) NOT NULL,
    take_profit_pct NUMERIC(6,3),
    max_open_usdc   NUMERIC(20,8) NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
COMMENT ON TABLE strategies IS '策略配置表（Web 后台 CRUD 目标）';
COMMENT ON COLUMN strategies.id              IS '策略唯一 ID（即 webhook payload 中的 strategy_id）';
COMMENT ON COLUMN strategies.symbol          IS '交易对，如 ETHUSDC';
COMMENT ON COLUMN strategies.leverage        IS '杠杆倍数，整数 1-125';
COMMENT ON COLUMN strategies.size_usdc       IS '每次下单名义价值（USDC）';
COMMENT ON COLUMN strategies.stop_loss_pct   IS '止损百分比，例 1.500 表示 1.5%';
COMMENT ON COLUMN strategies.take_profit_pct IS '止盈百分比，可空表示不挂止盈单';
COMMENT ON COLUMN strategies.max_open_usdc   IS '该策略允许的最大未平仓金额（USDC），风控用';
COMMENT ON COLUMN strategies.enabled         IS '是否启用：禁用后该策略所有信号均被拒绝';
COMMENT ON COLUMN strategies.created_at      IS '创建时间（UTC）';
COMMENT ON COLUMN strategies.updated_at      IS '更新时间（UTC）';

-- ========== signals ==========
CREATE TABLE signals (
    id              BIGSERIAL PRIMARY KEY,
    strategy_id     TEXT NOT NULL,
    symbol          TEXT NOT NULL,
    kind            signal_kind NOT NULL,
    signal_price    NUMERIC(20,8) NOT NULL,
    tv_timestamp_ms BIGINT NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    raw_payload     JSONB NOT NULL,
    client_ip       INET,
    decision        TEXT NOT NULL,
    decision_reason TEXT,
    trace_id        TEXT NOT NULL,
    UNIQUE (strategy_id, tv_timestamp_ms)
);
COMMENT ON TABLE signals IS '收到的所有 webhook 信号（审计 + 幂等事实源）';
COMMENT ON COLUMN signals.id              IS '自增主键';
COMMENT ON COLUMN signals.strategy_id     IS '触发该信号的策略 ID（不强制外键，允许接收未注册策略的信号以审计）';
COMMENT ON COLUMN signals.symbol          IS '交易对';
COMMENT ON COLUMN signals.kind            IS '信号方向：long/short/exit_long/exit_short';
COMMENT ON COLUMN signals.signal_price    IS 'TradingView 触发时的价格';
COMMENT ON COLUMN signals.tv_timestamp_ms IS 'TradingView 触发的毫秒时间戳，用于幂等与延迟统计';
COMMENT ON COLUMN signals.received_at     IS 'bot 收到信号的时间（UTC）';
COMMENT ON COLUMN signals.raw_payload     IS 'webhook 原始 JSON 全量留底';
COMMENT ON COLUMN signals.client_ip       IS '请求来源 IP';
COMMENT ON COLUMN signals.decision        IS '处理决定：accepted/duplicate/risk_denied/invalid/disarmed';
COMMENT ON COLUMN signals.decision_reason IS '决定的原因文案（拒绝时填写具体哪条规则）';
COMMENT ON COLUMN signals.trace_id        IS '贯穿整条处理链路的 trace id';

-- ========== virtual_positions ==========
CREATE TABLE virtual_positions (
    id                   BIGSERIAL PRIMARY KEY,
    strategy_id          TEXT NOT NULL REFERENCES strategies(id),
    symbol               TEXT NOT NULL,
    side                 TEXT NOT NULL,
    qty                  NUMERIC(20,8) NOT NULL,
    entry_signal_price   NUMERIC(20,8) NOT NULL,
    entry_fill_price     NUMERIC(20,8),
    entry_signal_id      BIGINT NOT NULL REFERENCES signals(id),
    entry_order_id       BIGINT,
    stop_order_id        BIGINT,
    backup_stop_order_id BIGINT,
    take_profit_order_id BIGINT,
    status               TEXT NOT NULL,
    opened_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at            TIMESTAMPTZ
);
COMMENT ON TABLE virtual_positions IS '虚拟持仓（按策略 ID 隔离记账，多个策略可共享真实账户）';
COMMENT ON COLUMN virtual_positions.id                   IS '自增主键';
COMMENT ON COLUMN virtual_positions.strategy_id          IS '所属策略 ID';
COMMENT ON COLUMN virtual_positions.symbol               IS '交易对';
COMMENT ON COLUMN virtual_positions.side                 IS '方向：long/short';
COMMENT ON COLUMN virtual_positions.qty                  IS '持仓币数量（已按 LOT_SIZE 取整）';
COMMENT ON COLUMN virtual_positions.entry_signal_price   IS '开仓信号价（TV 触发价）';
COMMENT ON COLUMN virtual_positions.entry_fill_price     IS '开仓实际成交均价（filled 后填充）';
COMMENT ON COLUMN virtual_positions.entry_signal_id      IS '触发开仓的 signals.id';
COMMENT ON COLUMN virtual_positions.entry_order_id       IS '开仓订单 orders.id';
COMMENT ON COLUMN virtual_positions.stop_order_id        IS '主止损单 orders.id（限价止损）';
COMMENT ON COLUMN virtual_positions.backup_stop_order_id IS '兜底止损单 orders.id（市价止损）';
COMMENT ON COLUMN virtual_positions.take_profit_order_id IS '止盈单 orders.id（可空）';
COMMENT ON COLUMN virtual_positions.status               IS '状态：opening/open/closing/closed';
COMMENT ON COLUMN virtual_positions.opened_at            IS '虚拟开仓时间';
COMMENT ON COLUMN virtual_positions.closed_at            IS '平仓时间（关闭后填）';

-- 同一策略同时只能有一个活跃仓位
CREATE UNIQUE INDEX uq_virtual_positions_active
    ON virtual_positions(strategy_id)
    WHERE status IN ('opening','open','closing');

-- ========== position_history ==========
CREATE TABLE position_history (
    id                      BIGSERIAL PRIMARY KEY,
    strategy_id             TEXT NOT NULL,
    symbol                  TEXT NOT NULL,
    side                    TEXT NOT NULL,
    qty                     NUMERIC(20,8) NOT NULL,
    entry_signal_price      NUMERIC(20,8) NOT NULL,
    entry_fill_price        NUMERIC(20,8) NOT NULL,
    exit_signal_price       NUMERIC(20,8) NOT NULL,
    exit_fill_price         NUMERIC(20,8) NOT NULL,
    pnl_usdc                NUMERIC(20,8) NOT NULL,
    pnl_pct                 NUMERIC(10,4) NOT NULL,
    fees_usdc               NUMERIC(20,8) NOT NULL DEFAULT 0,
    open_signal_to_fill_ms  INT,
    close_signal_to_fill_ms INT,
    open_slippage_bp        NUMERIC(10,4),
    close_slippage_bp       NUMERIC(10,4),
    close_reason            TEXT NOT NULL,
    duration_seconds        INT NOT NULL,
    opened_at               TIMESTAMPTZ NOT NULL,
    closed_at               TIMESTAMPTZ NOT NULL
);
COMMENT ON TABLE position_history IS '平仓归档（每次完整 开+平 的快照）';
COMMENT ON COLUMN position_history.id                      IS '自增主键';
COMMENT ON COLUMN position_history.strategy_id             IS '所属策略 ID';
COMMENT ON COLUMN position_history.symbol                  IS '交易对';
COMMENT ON COLUMN position_history.side                    IS '方向：long/short';
COMMENT ON COLUMN position_history.qty                     IS '币数量';
COMMENT ON COLUMN position_history.entry_signal_price      IS '开仓信号价';
COMMENT ON COLUMN position_history.entry_fill_price        IS '开仓成交均价';
COMMENT ON COLUMN position_history.exit_signal_price       IS '平仓信号价';
COMMENT ON COLUMN position_history.exit_fill_price         IS '平仓成交均价';
COMMENT ON COLUMN position_history.pnl_usdc                IS '盈亏（USDC，已扣手续费）';
COMMENT ON COLUMN position_history.pnl_pct                 IS '盈亏百分比';
COMMENT ON COLUMN position_history.fees_usdc               IS '本笔交易手续费总额（USDC）';
COMMENT ON COLUMN position_history.open_signal_to_fill_ms  IS '开仓延迟：从信号触发到成交的毫秒数';
COMMENT ON COLUMN position_history.close_signal_to_fill_ms IS '平仓延迟：从信号触发到成交的毫秒数';
COMMENT ON COLUMN position_history.open_slippage_bp        IS '开仓滑点（基点）：(成交价-信号价)/信号价*10000';
COMMENT ON COLUMN position_history.close_slippage_bp       IS '平仓滑点（基点）';
COMMENT ON COLUMN position_history.close_reason            IS '平仓原因：signal/stop_loss/take_profit/manual';
COMMENT ON COLUMN position_history.duration_seconds        IS '持仓时长（秒）';
COMMENT ON COLUMN position_history.opened_at               IS '开仓时间';
COMMENT ON COLUMN position_history.closed_at               IS '平仓时间';

CREATE INDEX idx_position_history_strategy_closed ON position_history(strategy_id, closed_at DESC);

-- ========== orders ==========
CREATE TABLE orders (
    id                  BIGSERIAL PRIMARY KEY,
    virtual_position_id BIGINT REFERENCES virtual_positions(id),
    strategy_id         TEXT NOT NULL,
    symbol              TEXT NOT NULL,
    side                TEXT NOT NULL,
    type                TEXT NOT NULL,
    purpose             TEXT NOT NULL,
    qty                 NUMERIC(20,8) NOT NULL,
    price               NUMERIC(20,8),
    stop_price          NUMERIC(20,8),
    client_order_id     TEXT NOT NULL UNIQUE,
    exchange_order_id   TEXT,
    status              order_status NOT NULL,
    filled_qty          NUMERIC(20,8) NOT NULL DEFAULT 0,
    avg_fill_price      NUMERIC(20,8),
    fees_usdc           NUMERIC(20,8) NOT NULL DEFAULT 0,
    submitted_at        TIMESTAMPTZ,
    filled_at           TIMESTAMPTZ,
    raw_response        JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
COMMENT ON TABLE orders IS '订单：与 Binance 真实订单一对一（client_order_id 是幂等键）';
COMMENT ON COLUMN orders.id                  IS '自增主键';
COMMENT ON COLUMN orders.virtual_position_id IS '所属虚拟仓位 ID（可空：dry_run 阶段或独立挂单）';
COMMENT ON COLUMN orders.strategy_id         IS '所属策略 ID';
COMMENT ON COLUMN orders.symbol              IS '交易对';
COMMENT ON COLUMN orders.side                IS '订单方向：BUY/SELL';
COMMENT ON COLUMN orders.type                IS '订单类型：MARKET/STOP/STOP_MARKET/TAKE_PROFIT_MARKET';
COMMENT ON COLUMN orders.purpose             IS '业务用途：entry/exit/stop/backup_stop/take_profit';
COMMENT ON COLUMN orders.qty                 IS '委托币数量';
COMMENT ON COLUMN orders.price               IS '限价（仅限价类型有值）';
COMMENT ON COLUMN orders.stop_price          IS '触发价（条件单）';
COMMENT ON COLUMN orders.client_order_id     IS '本地生成的客户端订单 ID（提交给 Binance 用于幂等）';
COMMENT ON COLUMN orders.exchange_order_id   IS 'Binance 返回的订单 ID';
COMMENT ON COLUMN orders.status              IS '订单状态';
COMMENT ON COLUMN orders.filled_qty          IS '已成交币数量';
COMMENT ON COLUMN orders.avg_fill_price      IS '成交均价';
COMMENT ON COLUMN orders.fees_usdc           IS '本订单手续费（USDC）';
COMMENT ON COLUMN orders.submitted_at        IS '提交到交易所时间';
COMMENT ON COLUMN orders.filled_at           IS '完全成交时间';
COMMENT ON COLUMN orders.raw_response        IS '最近一次交易所响应留底';
COMMENT ON COLUMN orders.created_at          IS '创建时间';
COMMENT ON COLUMN orders.updated_at          IS '更新时间';

CREATE INDEX idx_orders_position ON orders(virtual_position_id);
CREATE INDEX idx_orders_status ON orders(status) WHERE status IN ('pending','submitted','partial');

-- ========== system_state ==========
CREATE TABLE system_state (
    id              SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    armed           BOOLEAN NOT NULL DEFAULT false,
    armed_at        TIMESTAMPTZ,
    armed_by        TEXT,
    daily_pnl_usdc  NUMERIC(20,8) NOT NULL DEFAULT 0,
    daily_pnl_date  DATE NOT NULL DEFAULT CURRENT_DATE,
    breaker_tripped BOOLEAN NOT NULL DEFAULT false,
    breaker_reason  TEXT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
COMMENT ON TABLE system_state IS '系统全局状态（单行表）';
COMMENT ON COLUMN system_state.id              IS '固定 1，强制单行';
COMMENT ON COLUMN system_state.armed           IS '是否已启动交易（重启后默认 false）';
COMMENT ON COLUMN system_state.armed_at        IS '上次 arm 时间';
COMMENT ON COLUMN system_state.armed_by        IS '上次 arm 操作者';
COMMENT ON COLUMN system_state.daily_pnl_usdc  IS '当日累计盈亏（USDC）';
COMMENT ON COLUMN system_state.daily_pnl_date  IS '当日日期（用于翻天清零判断）';
COMMENT ON COLUMN system_state.breaker_tripped IS '熔断是否触发';
COMMENT ON COLUMN system_state.breaker_reason  IS '熔断原因';
COMMENT ON COLUMN system_state.updated_at      IS '更新时间';

INSERT INTO system_state (id) VALUES (1);

-- ========== users ==========
CREATE TABLE users (
    id            SMALLSERIAL PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
COMMENT ON TABLE users IS '后台用户（MVP 单用户）';
COMMENT ON COLUMN users.id            IS '自增主键';
COMMENT ON COLUMN users.username      IS '登录用户名';
COMMENT ON COLUMN users.password_hash IS 'bcrypt 哈希';
COMMENT ON COLUMN users.created_at    IS '创建时间';

-- ========== signals 索引（最后创建，依赖前面表已存在不必要，但保持紧邻表定义） ==========
CREATE INDEX idx_signals_received_at ON signals(received_at DESC);
CREATE INDEX idx_signals_strategy_id ON signals(strategy_id, received_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS system_state;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS position_history;
DROP TABLE IF EXISTS virtual_positions;
DROP TABLE IF EXISTS signals;
DROP TABLE IF EXISTS strategies;
DROP TYPE IF EXISTS bot_mode;
DROP TYPE IF EXISTS order_status;
DROP TYPE IF EXISTS signal_kind;
-- +goose StatementEnd

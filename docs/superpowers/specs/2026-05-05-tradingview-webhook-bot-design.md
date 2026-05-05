# TradingView Webhook 自动交易机器人 — 设计文档

- **日期**：2026-05-05
- **作者**：lizhaojie56@gmail.com
- **状态**：draft（待实施）
- **目标**：从 0 到 1 搭建一套接收 TradingView 策略信号、自动在 Binance 永续合约下单、带止损/风控/管理后台的 MVP 交易机器人

---

## 1. 目标与范围

### 1.1 业务目标

用户在 TradingView 上跑多个量化策略（如 `MACD宽松吞没V3.1`），策略触发 alert 时通过 webhook 推送信号到本系统，系统按预设配置自动在 Binance USDT/USDC-M 永续合约下单（含止损止盈），并提供 Web 后台管理策略与查看交易历史。

### 1.2 MVP 范围（本 spec 覆盖）

- ✅ 接收 TradingView webhook（鉴权、幂等、JSON 解析）
- ✅ 自动下单（市价开仓 + 双重止损 + 止盈）
- ✅ 多币种、多策略并行 + 虚拟仓位记账
- ✅ 4 项风控（单策略最大未平仓 / 总杠杆上限 / 日亏熔断 / IP 白名单）
- ✅ 三档运行模式（dry_run / testnet / live）+ 重启 disarm 手动 arm
- ✅ Web 后台 4 页（登录、策略管理、持仓历史、信号日志）
- ✅ 飞书 + Telegram 通知
- ✅ 故障恢复（启动对账 + 后台 OrderReconciler）

### 1.3 非范围（V2+ 再做）

- ❌ 滑点/延迟可视化分析图表
- ❌ 多地域低延迟部署（Tailscale 等）
- ❌ Python 策略回测平台
- ❌ 多用户、多 API Key 管理
- ❌ 跟踪止损（Trailing Stop）
- ❌ 物理子账号隔离

---

## 2. 关键技术决策

| # | 决策项 | 选择 | 理由 |
|---|--------|------|------|
| 1 | 交易所 | Binance USDT/USDC-M 永续合约 | 用户既有账户与策略 |
| 2 | 语言栈 | Go 核心 + Python 辅助（Python 后期） | Go 低延迟稳定，Python 后续做回测 |
| 3 | MVP 范围 | 核心交易闭环 | 避免一次造太多 |
| 4 | 持仓模型 | 多币种 + 多策略 + 虚拟仓位记账 | 同一币种多策略并行，仅 1 个 API Key |
| 5 | 信号契约 | 简化版（strategy_id, symbol, signal, price, ts, secret） | TV alert 越简单越不容易出错；策略参数预存 bot |
| 6 | 止损机制 | 双重止损（限价止损 + 市价兜底） | 跳空时仍能强平，正常时争更优价格 |
| 7 | 仓位规模 | 固定名义价值（USDC） | 简单、最常用 |
| 8 | 部署 | 本地 + Cloudflare Tunnel/ngrok（MVP） | 生产部署后置 |
| 9 | 数据库 | PostgreSQL（docker） | 与未来生产一致；列必须有中文备注 |
| 10 | 通知 | 飞书 + Telegram，Notifier 接口 | 双渠道冗余 |
| 11 | Web 后台 | 4 页（登录/策略/持仓/信号） | MVP 收敛 |
| 12 | 前端栈 | Go + HTMX + Tailwind | 单二进制、零打包 |
| 13 | 幂等 | 内存 LRU + DB 唯一键 | 双层防重 |
| 14 | 安全模式 | dry_run / testnet / live + 重启 disarm 手动 arm | 防误启动 |

---

## 3. 整体架构（方案 A：单体 Go 进程）

```
┌─────────────────────────────────────────┐
│   单个 Go 二进制（tvbot）                │
│  ┌─────────────────────────────────┐    │
│  │ HTTP Server                     │    │
│  │  ├─ /webhook/tv                 │    │
│  │  ├─ /admin/* (HTMX)             │    │
│  │  └─ /healthz                    │    │
│  ├─────────────────────────────────┤    │
│  │ Core Modules                    │    │
│  │  ├─ Idempotency (LRU + DB)      │    │
│  │  ├─ RiskGuard (4 规则)          │    │
│  │  ├─ Strategy Registry           │    │
│  │  ├─ PositionManager             │    │
│  │  ├─ Trader (Binance)            │    │
│  │  └─ Notifier (飞书/TG)          │    │
│  ├─────────────────────────────────┤    │
│  │ Background Workers              │    │
│  │  ├─ OrderReconciler (30s)       │    │
│  │  └─ DailyPnLResetter (每日 UTC) │    │
│  └─────────────────────────────────┘    │
└─────────────────────────────────────────┘
            │              │
            ▼              ▼
        PostgreSQL    Binance API
```

**依赖方向**：`web → application → domain ← infrastructure`。
domain 不依赖外部包，infrastructure 实现 domain 定义的 port 接口。

---

## 4. 项目结构

```
crypto/
├── cmd/
│   └── tvbot/main.go                # 进程入口、装配依赖
├── internal/
│   ├── config/                      # env + yaml + secrets
│   │   ├── config.go
│   │   └── config.yaml.example
│   ├── domain/                      # 纯业务（无外部依赖）
│   │   ├── signal/
│   │   ├── strategy/
│   │   ├── position/
│   │   └── order/
│   ├── application/                 # 用例编排
│   │   ├── ingest/                  # 信号摄入流程
│   │   ├── trade/                   # 下单/平仓
│   │   └── reconcile/               # 订单对账
│   ├── infrastructure/              # 外部适配
│   │   ├── binance/                 # REST + WS
│   │   ├── store/                   # pgx + sqlc
│   │   ├── notify/                  # 飞书 + Telegram
│   │   └── idempotency/             # LRU + DB
│   ├── risk/                        # 风控规则
│   │   ├── max_position.go
│   │   ├── total_leverage.go
│   │   ├── daily_loss_breaker.go
│   │   └── ip_whitelist.go
│   └── web/
│       ├── webhook/
│       ├── admin/
│       │   ├── handlers/
│       │   ├── templates/
│       │   └── static/
│       └── middleware/
├── migrations/                      # SQL 迁移（goose）
├── scripts/
│   ├── dev.sh
│   └── seed.sh
├── docker-compose.yml
├── Dockerfile
├── Makefile
├── .env.example
├── go.mod
└── README.md
```

### 4.1 模块边界

| 模块 | 职责 | 输入 | 输出 |
|------|------|------|------|
| `web/webhook` | 接收 TV 信号、鉴权 | HTTP POST | Signal 值对象 |
| `application/ingest` | 编排信号处理流程 | Signal | 下单决策 |
| `risk` | 4 个风控规则 Pipeline | Signal + 状态 | Allow / Deny |
| `domain/position` | 虚拟仓位记账 | 信号 + 当前持仓 | 仓位变更动作 |
| `application/trade` | 下单/平仓执行 | 仓位变更动作 | 订单 ID |
| `infrastructure/binance` | Binance API SDK 封装 | 内部订单结构 | 真实订单 |
| `application/reconcile` | 订单对账后台任务 | 周期触发 | 订单状态更新 |
| `infrastructure/notify` | 多渠道通知 | 通知消息 | 飞书/Telegram 推送 |

---

## 5. 数据模型（PostgreSQL）

**重要约束**：所有列必须用 `COMMENT ON COLUMN` 添加中文备注（迁移 SQL 中体现）。

### 5.1 枚举类型

```sql
CREATE TYPE signal_kind   AS ENUM ('long', 'short', 'exit_long', 'exit_short');
CREATE TYPE order_status  AS ENUM ('pending', 'submitted', 'partial', 'filled', 'canceled', 'rejected', 'expired');
CREATE TYPE bot_mode      AS ENUM ('dry_run', 'testnet', 'live');
```

### 5.2 表清单

7 张表：

1. **strategies** — 策略配置（Web 后台 CRUD 目标）
2. **signals** — 收到的所有信号（审计 + 幂等事实源）
3. **virtual_positions** — 虚拟持仓（按 strategy_id 隔离）
4. **position_history** — 平仓归档（含 PnL/滑点/延迟）
5. **orders** — 订单（与 Binance 一对一，client_order_id 幂等）
6. **system_state** — 单行表（armed / 当日 PnL / 熔断状态）
7. **users** — 单用户登录

### 5.3 关键约束

- `signals(strategy_id, tv_timestamp_ms)` 唯一索引 → 幂等事实源
- `virtual_positions` partial unique index `WHERE status IN ('opening','open','closing')` → 强制同策略同时只有一个活跃仓位
- `orders.client_order_id` 全局唯一 → Binance 幂等
- `system_state` 单行表（`CHECK (id = 1)`）→ 全局状态唯一

### 5.4 完整 schema

完整 SQL（含所有列的 `COMMENT ON COLUMN` 中文备注）见实施阶段 `migrations/0001_init.sql`。本节列出表结构骨架供参考：

```sql
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

CREATE TABLE virtual_positions (
    id              BIGSERIAL PRIMARY KEY,
    strategy_id     TEXT NOT NULL REFERENCES strategies(id),
    symbol          TEXT NOT NULL,
    side            TEXT NOT NULL,
    qty             NUMERIC(20,8) NOT NULL,
    entry_signal_price NUMERIC(20,8) NOT NULL,
    entry_fill_price   NUMERIC(20,8),
    entry_signal_id BIGINT NOT NULL REFERENCES signals(id),
    entry_order_id  BIGINT,
    stop_order_id   BIGINT,
    backup_stop_order_id BIGINT,
    take_profit_order_id BIGINT,
    status          TEXT NOT NULL,
    opened_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at       TIMESTAMPTZ
);

CREATE TABLE position_history (
    id              BIGSERIAL PRIMARY KEY,
    strategy_id     TEXT NOT NULL,
    symbol          TEXT NOT NULL,
    side            TEXT NOT NULL,
    qty             NUMERIC(20,8) NOT NULL,
    entry_signal_price NUMERIC(20,8) NOT NULL,
    entry_fill_price   NUMERIC(20,8) NOT NULL,
    exit_signal_price  NUMERIC(20,8) NOT NULL,
    exit_fill_price    NUMERIC(20,8) NOT NULL,
    pnl_usdc        NUMERIC(20,8) NOT NULL,
    pnl_pct         NUMERIC(10,4) NOT NULL,
    fees_usdc       NUMERIC(20,8) NOT NULL DEFAULT 0,
    open_signal_to_fill_ms  INT,
    close_signal_to_fill_ms INT,
    open_slippage_bp        NUMERIC(10,4),
    close_slippage_bp       NUMERIC(10,4),
    close_reason    TEXT NOT NULL,
    duration_seconds INT NOT NULL,
    opened_at       TIMESTAMPTZ NOT NULL,
    closed_at       TIMESTAMPTZ NOT NULL
);

CREATE TABLE orders (
    id              BIGSERIAL PRIMARY KEY,
    virtual_position_id BIGINT REFERENCES virtual_positions(id),
    strategy_id     TEXT NOT NULL,
    symbol          TEXT NOT NULL,
    side            TEXT NOT NULL,
    type            TEXT NOT NULL,
    purpose         TEXT NOT NULL,
    qty             NUMERIC(20,8) NOT NULL,
    price           NUMERIC(20,8),
    stop_price      NUMERIC(20,8),
    client_order_id TEXT NOT NULL UNIQUE,
    exchange_order_id TEXT,
    status          order_status NOT NULL,
    filled_qty      NUMERIC(20,8) NOT NULL DEFAULT 0,
    avg_fill_price  NUMERIC(20,8),
    fees_usdc       NUMERIC(20,8) NOT NULL DEFAULT 0,
    submitted_at    TIMESTAMPTZ,
    filled_at       TIMESTAMPTZ,
    raw_response    JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

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
INSERT INTO system_state (id) VALUES (1);

CREATE TABLE users (
    id              SMALLSERIAL PRIMARY KEY,
    username        TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 索引
CREATE INDEX idx_signals_received_at ON signals(received_at DESC);
CREATE INDEX idx_signals_strategy_id ON signals(strategy_id, received_at DESC);
CREATE UNIQUE INDEX uq_virtual_positions_active
    ON virtual_positions(strategy_id)
    WHERE status IN ('opening', 'open', 'closing');
CREATE INDEX idx_position_history_strategy_closed ON position_history(strategy_id, closed_at DESC);
CREATE INDEX idx_orders_position ON orders(virtual_position_id);
CREATE INDEX idx_orders_status ON orders(status) WHERE status IN ('pending', 'submitted', 'partial');
```

迁移文件中**每一列**必须配 `COMMENT ON COLUMN <table>.<col> IS '<中文说明>';`。

---

## 6. 信号契约

### 6.1 TradingView Webhook payload

```json
{
  "strategy_id": "macd_loose_engulf_v3.1_eth_high",
  "symbol": "ETHUSDC",
  "signal": "Long",
  "price": "2312.14",
  "timestamp": 1714723504000,
  "secret": "<shared-secret>"
}
```

| 字段 | 类型 | 含义 |
|------|------|------|
| `strategy_id` | string | 策略唯一 ID（虚拟仓位的 key） |
| `symbol` | string | 交易对 |
| `signal` | string | `Long` / `Short` / `Exit Long` / `Exit Short` |
| `price` | string（数字） | TV 触发时价格 |
| `timestamp` | int64 | TV 触发毫秒时间戳（用于幂等 + 延迟统计） |
| `secret` | string | 鉴权密钥（与 bot 配置比对） |

策略参数（leverage, size_usdc, stop_loss_pct 等）**不在 payload 中**，由 bot 端按 `strategy_id` 查 strategies 表。

### 6.2 TradingView Alert message 模板

```
{"strategy_id":"macd_loose_engulf_v3.1_eth_high","symbol":"{{ticker}}","signal":"{{strategy.order.action}}","price":"{{close}}","timestamp":{{time}},"secret":"<secret>"}
```

---

## 7. 核心数据流

详细 6 步流程：

1. **HTTP 入口**：trace_id 生成 → 限流 → IP 白名单 → secret 校验 → JSON 解析
2. **幂等检查**：内存 LRU 命中即返回 200；miss 进入下一步
3. **DB 事务**：插入 signals（ON CONFLICT DO NOTHING）→ 加载 strategy / system_state / 当前 virtual_position
4. **风控 Pipeline**（按序）：armed → breaker → 信号语义合法 → 单策略未平仓上限 → 总杠杆上限 → 日亏熔断
5. **执行决策**（持仓 × 信号 → 动作）：
   ```
   空 + Long       → open_long
   空 + Short      → open_short
   空 + Exit *     → no_op
   Long + Exit Long → close
   Long + Long     → no_op (已是同向)
   Long + Short    → close + open_short
   Short + Exit Short → close
   Short + Long    → close + open_long
   ```
6. **下单执行**：
   - **开仓**：qty 计算（按 LOT_SIZE floor）→ 市价开仓 → 等 fill（≤3s）→ 立即下三个保护单（主止损 + 兜底市价止损 + 止盈）→ 入库 → 通知
   - **平仓**：撤保护单（并行）→ 市价平仓 → 计算 PnL/滑点/延迟 → 写 position_history → 更新 daily_pnl → 通知

**响应模式**：异步——HTTP 入口立即返回 200 OK 后，Step 5/6 在带 timeout + recover 的 goroutine 内执行，避免 TV 重试。

**模式分支**：
- `dry_run`：Step 6 mock，只在 orders 表写 `status='dry_run'`
- `testnet`：走 `https://testnet.binancefuture.com`
- `live`：真实账户

---

## 8. 风控 Pipeline

按顺序执行，任一拒绝立即返回。

| # | 规则 | 拒绝条件 | 配置位置 |
|---|------|---------|---------|
| A | armed | `system_state.armed = false` | 后台手动按钮 |
| B | breaker | `system_state.breaker_tripped = true` | 自动触发 |
| C | 信号语义 | 持仓为空收到 Exit 信号 | 内置 |
| D | 单策略最大未平仓 | 当前未平仓金额（含此次）> `strategy.max_open_usdc` | strategies 表 |
| E | 全局总杠杆上限 | `Σ(虚拟仓位名义价值) / 账户权益` > 配置阈值 | env / yaml |
| F | 日亏熔断 | `system_state.daily_pnl_usdc` < -`max_daily_loss_usdc` → 自动设置 `breaker_tripped=true` | env / yaml |

**IP 白名单**：在 HTTP 入口 middleware 实施（非风控 pipeline 步骤），TradingView 官方 IP 段 + 本地白名单 IP（开发用）。

**daily PnL 重置**：每日 UTC 00:00 由 DailyPnLResetter 后台任务自动清零（由 `daily_pnl_date` 比对当前日期触发，任何 PnL 更新前都会先检查日期翻天）。

---

## 9. 故障恢复与不变量

### 9.1 不变量（INVARIANTS）

代码持续校验：

1. 每个 `strategy_id` 同时只能有一个 active 仓位（partial unique index 强制）
2. `virtual_position` 状态机：`opening → open → closing → closed`，绝不倒退
3. active 仓位必须挂着主止损单（OrderReconciler 监控）
4. `orders.client_order_id` 全局唯一

### 9.2 三个棘手场景

**场景 A：开仓成功但下止损单失败**
1. 立即重试 1 次
2. 仍失败 → 立即下兜底市价单平仓（fail-safe close）
3. 飞书 + Telegram 双渠道高优通知
4. 该策略自动 `enabled = false`

宁可不赚这一单，绝不让无止损仓位裸奔。

**场景 B：下单 API 超时（不知道是否真下单了）**
1. 用预生成 `client_order_id` 查询订单
2. 已收单 → 按真实状态推进
3. 没收到 → 重试
4. 查询也超时 → 进 OrderReconciler 队列后台对账

`client_order_id` 是幂等的关键，永远不在不确定状态下重复下单。

**场景 C：bot 崩溃重启**

启动恢复流程：

1. 加载所有 `status IN ('opening','open','closing')` 的 virtual_positions
2. 对每个：
   - 拉 Binance positionRisk → 真实仓位
   - 拉所有未完成 open orders
   - 对账：
     - 仓位匹配 + 三保护单完整 → `status='open'`
     - 仓位匹配 + 缺保护单 → 立即补单
     - 仓位 qty 不一致 → 写"对账异常"表 + 通知 + 自动 disarm（手动介入）
     - 真实仓位为空但 DB 有 → 视为止损已触发，按平仓处理（计算 PnL）
3. 恢复完成 → `armed=false`（必须用户手动 arm）

### 9.3 OrderReconciler（每 30s）

- 扫所有 `status IN ('pending','submitted','partial')` 订单
- 用 `client_order_id` 查 Binance
- 同步 `status / filled_qty / avg_fill_price`
- 检测保护单异常取消 → 立即补挂 + 高优先级告警

### 9.4 错误分类

| 类别 | 例 | 处理 |
|------|-----|------|
| 客户端错误 | JSON 错、secret 错、IP 不白 | 4xx 返回 + 落库 |
| 业务规则拒绝 | 策略 disabled、风控拒 | 200 OK 落库（不让 TV 重试） |
| 临时网络错误 | Binance 超时 | 指数退避重试 3 次（1s/2s/4s） |
| 业务错误 | -2010 余额不足、-1111 精度错 | 不重试 + 通知 + 触发熔断警告 |
| 致命错误 | DB 挂 | 进程退出（docker 重启） |
| 未知 panic | 任何 goroutine | recover + 通知 + 不退出，相关策略 disarm |

### 9.5 优雅关闭

收到 SIGTERM：停止接收新 webhook → 等待执行中流程完成（≤30s）→ 关闭 DB → 退出。**不在关闭时撤单或平仓**——重启后由恢复流程处理。

---

## 10. Web 后台

### 10.1 路由

```
GET  /login                    登录
POST /login
POST /logout
GET  /                         → /strategies
GET  /strategies               策略列表
GET  /strategies/new           新增 modal
POST /strategies
GET  /strategies/:id/edit
PUT  /strategies/:id
DELETE /strategies/:id         软删
POST /strategies/:id/toggle    启用/禁用
GET  /positions                持仓 + 最近 50 历史
GET  /positions/history        分页历史（含筛选）
POST /positions/:id/close      手动紧急平仓
GET  /signals                  信号日志
GET  /signals/:id              详情
POST /system/arm               启动交易
POST /system/disarm            停止
POST /system/breaker/reset     重置熔断
GET  /healthz                  健康检查（不鉴权）
```

### 10.2 鉴权

- 单用户 + bcrypt
- 登录后 secure cookie session（gorilla/sessions 或 alexedwards/scs）
- `/strategies`, `/positions`, `/signals`, `/system/*` 走 auth middleware
- `/webhook/tv` 用 secret + IP 白名单（不走 cookie）

### 10.3 顶部状态栏（每页 HTMX 5s 刷新）

```
[模式: LIVE] [armed: ✅] [熔断: ✅] [今日 PnL: +123.45 USDC] [活跃仓位: 3]
```

armed 状态用大色块（红=disarmed，绿=armed）。

### 10.4 模板组织

```
internal/web/admin/templates/
├── layouts/
│   ├── base.html
│   └── auth.html
├── partials/
│   ├── status_bar.html
│   └── strategy_row.html
└── pages/
    ├── login.html
    ├── strategies/{index,new,edit}.html
    ├── positions/{index,history}.html
    └── signals/{index,detail}.html
```

---

## 11. 配置

### 11.1 环境变量

```
BOT_MODE=dry_run|testnet|live
DATABASE_URL=postgres://tvbot:tvbot@localhost:5432/tvbot?sslmode=disable
BINANCE_API_KEY=xxx
BINANCE_API_SECRET=xxx
WEBHOOK_SECRET=xxx
SESSION_SECRET=xxx
HTTP_LISTEN=0.0.0.0:8080
LOG_LEVEL=info
TZ=UTC
```

### 11.2 yaml 配置（`config/config.yaml`）

```yaml
risk:
  max_total_leverage: 3.0           # 总杠杆上限
  max_daily_loss_usdc: 500          # 日亏熔断阈值
ip_whitelist:
  - 52.89.214.238                   # TV 官方
  - 34.212.75.30
  - 54.218.53.128
  - 52.32.178.7
  - 127.0.0.1                       # 本地开发
notifier:
  feishu:
    webhook_url: ""
    enabled: true
  telegram:
    bot_token: ""
    chat_id: ""
    enabled: true
binance:
  base_url_live:    "https://fapi.binance.com"
  base_url_testnet: "https://testnet.binancefuture.com"
  recv_window_ms:   5000
  order_timeout_ms: 3000
reconciler:
  interval_seconds: 30
```

启动时 banner 用 ASCII 大字打印 `BOT_MODE` 和 `armed` 状态，颜色按模式区分。

---

## 12. 测试策略

### 12.1 测试金字塔

- **单元测试**（100+，覆盖 domain + risk）
- **集成测试**（10-15，真 PG via `dockertest` + mock Binance via `httptest`）
- **E2E**（1-2 个，dry_run 模式跑完整链路）

### 12.2 单元覆盖重点

| 模块 | 测什么 |
|------|--------|
| `domain/position` | (持仓 × 信号) → 动作 8 种组合全覆盖 |
| `domain/signal` | JSON 解析 + 边界（缺字段、负价、未来 ts） |
| `risk/*` | 4 规则各自允/拒 case |
| `infrastructure/binance` | qty floor LOT_SIZE、price floor tick_size |
| `infrastructure/idempotency` | LRU 命中/miss/容量 |

### 12.3 集成测试（关键）

§9.2 三个故障场景每个都必须有对应集成测试：
- 开仓成功但下止损失败 → 自动平仓 + 策略 disable
- 下单超时 → 用 client_order_id 查询恢复
- 重启对账 → 各种状态分支

**不 mock 数据库**——避免"mock 通过但生产挂"的坑。

### 12.4 E2E

启动完整 bot（dry_run + ephemeral PG）→ POST 一条 Long 信号 → 断言 signals/virtual_positions/orders 表状态 → POST 一条 Exit Long → 断言 position_history 完整闭环 + PnL 计算正确。

### 12.5 上线前手动 checklist

1. dry_run：本地 curl POST → 验证全链路
2. testnet：Binance Futures Testnet 跑真信号开/止损/平
3. 手动触发故障场景 A/B/C
4. 手动测熔断：触发当日亏损上限 → 后续信号被拒
5. live + 最小 size_usdc 跑真单：API Key 设 IP 白名单 + 仅交易权限不授提币

---

## 13. 部署（MVP）

### 13.1 docker-compose（本地开发）

```yaml
version: "3.9"
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: tvbot
      POSTGRES_PASSWORD: tvbot
      POSTGRES_DB: tvbot
    ports: ["5432:5432"]
    volumes: ["./.data/pg:/var/lib/postgresql/data"]
  bot:
    build: .
    environment:
      BOT_MODE: dry_run
      DATABASE_URL: postgres://tvbot:tvbot@postgres:5432/tvbot?sslmode=disable
    ports: ["8080:8080"]
    depends_on: [postgres]
```

### 13.2 公网暴露

开发期用 `cloudflared tunnel` 或 `ngrok http 8080`，把 `https://<sub>.ngrok.app/webhook/tv` 配到 TradingView Alert URL。

### 13.3 生产部署（V2，本 spec 不实施）

候选：单台云 VPS + Caddy 反代（自动 HTTPS） + docker-compose；后续可换 Sealos / Railway / 自建 K8s。

---

## 14. 开放问题（实施时再确认）

- IP 白名单的 TradingView 官方 IP 列表需要在实施时从最新文档拉取并写入 `config.yaml`
- Telegram bot token 是否每次启动都要 `getMe` 验证连通性
- 是否在 Web 后台暴露"重新加载策略"按钮（避免重启）

---

## 15. 实施顺序建议（给写 plan 的参考）

1. 项目骨架 + go.mod + 目录创建
2. 配置加载 + 日志 + trace_id
3. PostgreSQL migrations（含全部列备注） + sqlc/pgx 接入
4. domain 层（signal / strategy / position / order）+ 单测
5. risk pipeline + 单测
6. infrastructure/idempotency
7. Binance SDK 封装（先 dry_run mock + testnet）
8. application/ingest（信号摄入完整流程）
9. application/trade（开仓/平仓/止损）
10. infrastructure/notify（飞书 + Telegram）
11. application/reconcile + 启动恢复流程
12. web/webhook + middleware
13. web/admin（4 个页面 + auth）
14. docker-compose + dev 脚本
15. 集成测试 + E2E
16. 手动 checklist 跑通

---

**EOF**

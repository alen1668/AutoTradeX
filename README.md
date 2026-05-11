# tvbot — TradingView Webhook 自动交易机器人

一个**单 Go 二进制** + PostgreSQL 的自动交易系统：接收 TradingView 策略信号 → 风控 → 在币安 USDT/USDC-M 永续合约上自动下单。带 HTMX Web 后台，所有运行时配置可在浏览器里改。

---

## 目录

1. [它能做什么](#它能做什么)
2. [需要先注册的账号](#需要先注册的账号)
3. [快速开始（5 分钟跑通）](#快速开始5-分钟跑通)
4. [给完全新手的 TradingView 配置教程](#给完全新手的-tradingview-配置教程)
5. [运行模式（testnet / live）](#运行模式)
6. [实盘（live）运行手册](#实盘live运行手册)
7. [运维操作](#运维操作)
8. [配置参考](#配置参考)
9. [部署到生产](#部署到生产)
10. [测试](#测试)
11. [架构](#架构)
12. [常见问题](#常见问题-faq)

---

## 它能做什么

- 接 TradingView 任何策略的 alert（Pine Script 内置或自写）
- 自动在币安永续合约下单，**支持双重止损**（限价止损 + 市价兜底）+ 止盈
- **多策略并行**：同一个币种可同时跑多个策略，虚拟仓位记账，共用 1 个 API Key
- 4 项风控：单策略最大未平仓金额、全局总杠杆上限、日亏熔断、IP 白名单
- 两档运行模式：testnet（币安测试网，推荐先跑）/ live（实盘）
- 重启自动 disarm，必须手动点「启动交易」才会接单
- 飞书 + Telegram 双渠道告警
- Web 后台改配置实时生效（部分需重启，UI 有标注）
- **`/eval` 评估面板**：浏览灰度期 score-bucket × 实际 PnL 报告；浏览 / 查看历次 replay 实验（agent prompt A/B 调优用，结果自动入库归档）

---

## 需要先注册的账号

在动手之前，先把这几个账号都准备好。**强烈建议从测试网开始**，等熟悉了再换实盘。

### 1. 币安（Binance）—— 必须

| 用途 | 网址 | 说明 |
|------|------|------|
| **测试网注册（强烈推荐先用这个）** | https://testnet.binancefuture.com/ | 用测试 USDT 玩，亏了不心疼，没有 KYC 麻烦 |
| **测试网 API 申请** | https://testnet.binancefuture.com/en/futures/BTCUSDT （登录后右上角「API Key」） | 直接生成，无需邮箱验证 |
| **实盘注册** | https://www.binance.com/zh-CN | 中国大陆访问需要科学上网 |
| **实盘 API 管理** | https://www.binance.com/zh-CN/my/settings/api-management | 创建 API → **必须勾选「启用合约」**，**绝对不要勾「启用提现」** |
| **永续合约开通** | 实盘需先在 https://www.binance.com/zh-CN/futures 「开通合约」 | 通过简单测试题，开通 USDT-M Futures |
| **API 申请文档** | https://developers.binance.com/docs/zh-CN/derivatives/usds-margined-futures/general-info | 官方 API 文档（看不懂可忽略，bot 已封装） |

**测试网拿测试币（faucet）**：登录 testnet.binancefuture.com 后，会自动赠送一些测试 USDT（约 100,000 USDT）。如果用完了，去 https://testnet.binancefuture.com/en/balance/funds 点「Free $1000」按钮领取。

**API Key 安全设置（实盘强制要求）**：

1. 创建 API 时勾选项：
   - ✅ **Enable Reading**（读取，必须）
   - ✅ **Enable Futures**（合约交易，必须）
   - ❌ **Enable Spot & Margin Trading**（不用，不勾）
   - ❌ **Enable Withdrawals**（提现，**绝对不勾**！防止 API 被盗后资金被卷走）
2. **IP 白名单**：填写你部署 bot 的服务器公网 IP。本地测试可暂时勾「Unrestricted」，但生产环境**必须**绑定 IP。
3. **Secret Key 只在创建时显示一次**，立刻复制保存到 bot 后台 /settings → 币安 API 区块。

### 2. TradingView —— 必须

| 用途 | 网址 | 说明 |
|------|------|------|
| **注册账号** | https://www.tradingview.com/signup/ | 免费 |
| **套餐升级（webhook 必须）** | https://www.tradingview.com/pricing/ | **免费版不支持 webhook**，最低需要 Essential（约 $14.95/月）才能用 webhook 告警 |
| **Pine Script 文档** | https://cn.tradingview.com/pine-script-docs/ | 写自定义策略时参考 |

> 💡 **TradingView 免费版能玩 bot 吗**？可以，但只能用 curl 手动模拟 webhook 测试。要让策略自动触发必须 Essential 及以上套餐。

### 3. Cloudflare —— 必须（用于把本地 bot 暴露到公网）

| 用途 | 网址 | 说明 |
|------|------|------|
| **注册账号（绑域名时需要）** | https://dash.cloudflare.com/sign-up | 免费 |
| **Cloudflare Tunnel 文档** | https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/ | 临时 tunnel 不需要账号；长期用建议绑域名 |
| **cloudflared 下载** | https://github.com/cloudflare/cloudflared/releases | macOS 推荐用 `brew install cloudflared` |

> 💡 **替代方案**：如果你不想用 Cloudflare，可以用 ngrok：
> - 注册 https://dashboard.ngrok.com/signup
> - 装 https://ngrok.com/download
> - 个人免费版 1 个 tunnel 够用

### 4. 飞书机器人 —— 可选（用于通知）

| 用途 | 网址 | 说明 |
|------|------|------|
| **创建自定义机器人 webhook** | 飞书 App 内：群聊 → 设置 → 群机器人 → 添加机器人 → 自定义机器人 | [官方文档](https://www.feishu.cn/hc/zh-CN/articles/360024984973) |
| **机器人配置文档** | https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot | 添加后会得到一个 webhook URL，填到 bot 后台 /settings |

### 5. Telegram —— 可选（用于通知）

| 用途 | 网址 | 说明 |
|------|------|------|
| **创建 Bot** | 在 Telegram 里联系 [@BotFather](https://t.me/BotFather) 发 `/newbot` | 跟着提示走，最后会得到一个 token |
| **找你的 chat_id** | 在 Telegram 里联系 [@userinfobot](https://t.me/userinfobot) 发任意消息 | 它会回你 chat_id（或群组 ID） |
| **Bot API 文档** | https://core.telegram.org/bots/api | 进阶参考 |

> 💡 中国大陆访问 Telegram 需要科学上网。

### 6. VPS / 云服务器 —— 上线生产时

任选一家：

| 厂商 | 网址 | 推荐场景 |
|------|------|---------|
| **DigitalOcean** | https://www.digitalocean.com/ | $4/月起，支持港日新加坡机房，币安延迟低 |
| **Vultr** | https://www.vultr.com/ | $2.50/月起，机房更多 |
| **AWS Lightsail** | https://aws.amazon.com/lightsail/ | $3.50/月起 |
| **腾讯云轻量服** | https://cloud.tencent.com/product/lighthouse | 国内用户友好，但访问 Binance 需选海外节点 |
| **阿里云 ECS** | https://www.aliyun.com/product/ecs | 同上 |

**最低配置建议**：1 vCPU + 1GB RAM + 25GB 磁盘 = 够用。bot 进程极轻，瓶颈是 PostgreSQL。

---

## 快速开始（5 分钟跑通）

### 前置条件

- **macOS / Linux**（Windows 用 WSL2）
- **Go 1.23+**：`go version` 检查；没有的话 `brew install go` 或 https://go.dev/dl/
- **Docker Desktop / OrbStack / colima**：用于跑 PostgreSQL；OrbStack 推荐（`brew install --cask orbstack`）

### 一键启动

```bash
# 1. 克隆代码
git clone git@github.com:alen1668/AutoTradeX.git tvbot
cd tvbot

# 2. 启动 PostgreSQL（容器）
make pg-up
make migrate-up

# 3. 准备配置文件
cp config/config.yaml.example config/config.yaml
cp .env.example .env

# 4. 编辑 .env 至少改下面这几个值
$EDITOR .env
```

`.env` 里**必须改**的（其他可以先用默认）：

```bash
BOT_MODE=testnet                                      # 第一次跑用 testnet，币安测试网假币
WEBHOOK_SECRET=随便起个长一点的密码-比如至少20位         # 等下要在 TradingView 里填的密钥
SESSION_SECRET=please-change-me-please-change-me      # 至少 32 字节，浏览器登录 cookie 加密用
```

```bash
# 5. 编译并启动
make build
./bin/tvbot
```

启动成功会打印：

```
================================
        tvbot starting          
  mode: testnet
  armed: false (run /system/arm to enable)
================================
{"level":"info","message":"http listening","addr":"0.0.0.0:8080"}
```

### 创建管理员账号

**另开一个终端**（让 bot 继续在前台跑）：

```bash
./bin/tvbot seed-user
# Username: alen        ← 自己起
# Password: ******      ← 至少 8 位
```

### 浏览器登录

打开 http://localhost:8080/login，用刚才创建的账号登录。看到导航栏有「策略 / 持仓 / 信号 / 系统 / 统计 / 配置」就成功了。

> 如果看到「启动 bot 后端」的命令需要环境变量太多，可以这样一行起来：
> ```bash
> BOT_MODE=testnet \
> DATABASE_URL=postgres://tvbot:tvbot@localhost:5432/tvbot?sslmode=disable \
> WEBHOOK_SECRET=$(grep WEBHOOK_SECRET .env | cut -d= -f2) \
> SESSION_SECRET=$(grep SESSION_SECRET .env | cut -d= -f2) \
> ./bin/tvbot
> ```

---

## 给完全新手的 TradingView 配置教程

> 看这部分前请确保你**已经登录到 bot 后台**（http://localhost:8080）。

整个数据流是这样：

```
TradingView 服务器（云端） ─── HTTPS POST ──► 你的公网 URL ──► 你的 bot ──► 币安
```

TradingView 在云端跑，它**不能**直接访问你电脑上的 `localhost:8080`，必须给它一个公网 HTTPS 地址。下面我们一步步来。

---

### 第 1 步：把本地 bot 暴露到公网（Cloudflare Tunnel）

Cloudflare 提供**免费**的 tunnel 服务，零配置就能给你一个临时公网域名。

**1.1 装 cloudflared**：

```bash
# macOS
brew install cloudflared

# 验证装上了
cloudflared --version
```

**1.2 启动 tunnel**（**保持这个终端不要关**）：

```bash
cloudflared tunnel --url http://127.0.0.1:8080
```

输出里找到这一行（每次启动都会变）：

```
2026-05-06T02:30:15Z INF +--------------------------------------------------------------------------------------------+
2026-05-06T02:30:15Z INF |  Your quick Tunnel has been created! Visit it at (it may take some time to be reachable):  |
2026-05-06T02:30:15Z INF |  https://random-words-xxxx-yyyy.trycloudflare.com                                          |
2026-05-06T02:30:15Z INF +--------------------------------------------------------------------------------------------+
```

**`https://random-words-xxxx-yyyy.trycloudflare.com` 就是你的 webhook 公网入口**。复制它。

**1.3 验证**：另开一个终端测试：

```bash
curl https://random-words-xxxx-yyyy.trycloudflare.com/healthz
# 应该返回: ok
```

如果返回 `ok` 就说明 TV → tunnel → bot 的链路通了。

> 💡 **关于 trycloudflare 临时域名**：免费版每次重启 cloudflared 域名会变。长期用建议绑你自己的域名，参考 [Cloudflare Tunnel 文档](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)。或者用 ngrok（`brew install ngrok` + `ngrok http 8080`）。

---

### 第 2 步：bot 后台准备工作

#### 2.1 关闭 IP 白名单（开发期）

打开 http://localhost:8080/settings → **IP 白名单** 区块。

**把 textarea 里的内容全部清空** → 点「保存白名单」。

> ⚠️ 为什么要清空？因为 webhook 经过 Cloudflare Tunnel 转发，到 bot 时**源 IP 是 Cloudflare 的服务器，不是 TradingView 真实 IP**。如果保留 TV 官方 IP 白名单，请求会被拒绝。
>
> 上线后想加白名单，需要把 Cloudflare 的反向代理 IP 段加上，**但这种情况下其实 secret 鉴权就够安全了**。

#### 2.2 设置 Webhook Secret

打开 http://localhost:8080/settings → **高级配置** 区块。

在 **Webhook Secret** 输入框填一个**强密码**（至少 20 位随机字符）。这个值 TradingView 那边等会要填一样的。点「保存高级配置」。

> ⚠️ 别用简单密码（`123456`、`password` 之类）。你的 webhook URL 是公网可达的，secret 是唯一的鉴权防线。
>
> 生成强密码：`openssl rand -hex 24`

#### 2.3 创建你的第一个策略

打开 http://localhost:8080/strategies → 「**新增**」按钮。

填写表单（示例：跑 ETH 的策略）：

| 字段 | 示例值 | 说明 |
|------|------|------|
| 策略 ID | `macd_eth_long` | 唯一标识，在 TradingView 那边要填一模一样 |
| 币种 | `ETHUSDC` | 必须是 Binance USDT-M 永续支持的币种 |
| 杠杆 | `5` | 1-125，建议先用 3-5 倍 |
| 名义价值 (USDC) | `100` | 每次开仓的本金（不含杠杆放大） |
| 止损 % | `1.5` | 触发价 = 入场价 ± 1.5% |
| 止盈 % | `3.0` | 留空表示不挂止盈单 |
| 单策略未平仓上限 (USDC) | `500` | 防止单策略风险过度集中 |
| 启用 | ✓ | 勾上 |

点「保存」。

#### 2.4 启动交易

回到 http://localhost:8080/system → 看到大色块写「**已停止**」。

点 **绿色按钮「启动交易（arm）」**。状态变成「**已启动**」。

> 🛡️ 每次重启 bot 进程都会自动 disarm（崩溃保护），必须手动 arm 一次才会真接单。这是设计的安全机制。

---

### 第 3 步：在 TradingView 网站上配置告警

打开 https://tradingview.com 登录账号。

#### 3.1 打开你想用的图表

随便打开一个币种（比如 ETHUSDC）的图表。**确认图表上面已经加载了你的策略**——左上角策略图层（默认 Pine Script 内置策略，或者你自己写的策略）。

> 没有自己的策略？可以先用 TradingView 内置的一个简单策略测试，比如「Bollinger Bands Strategy」（图表 → 指标 → 搜索「Bollinger Bands Strategy」→ 添加到图表）。

#### 3.2 创建告警

按 **Alt+A**（Windows）或 **Option+A**（Mac），或点图表右上角 **🔔 闹钟图标** → **创建告警**。

弹出的对话框分为「设置（Settings）」「通知（Notifications）」「条件（Condition）」三个 tab。

#### 3.3 「条件 (Condition)」tab

第一个下拉框选 **你刚加的策略名**。

第二个下拉框选触发条件：
- **「Order Fills Only」**（推荐）：策略每次下单（开仓/平仓）都触发，最简单
- 也可以选具体的 `entry long`/`exit long` 等

#### 3.4 「通知 (Notifications)」tab

向下滚动找到 **「Webhook URL」** 选项 → **打勾启用**。

URL 填：

```
https://random-words-xxxx-yyyy.trycloudflare.com/webhook/tv
```

⚠️ **末尾的 `/webhook/tv` 路径不要漏！**

> 💡 **Webhook URL 是 TradingView 付费功能**：免费账号没有 webhook，至少需要 Pro 套餐（最便宜约 $14.95/月）。不想付费可以先在 testnet 模式 + curl 手动测试（见下面 FAQ）。

#### 3.5 「设置 (Settings)」tab → 消息（Message）

把消息框的内容**完全替换**为这段 JSON：

```json
{
  "strategy_id": "macd_eth_long",
  "symbol": "{{ticker}}",
  "signal": "{{strategy.order.action}}",
  "position_size": "{{strategy.position_size}}",
  "price": "{{close}}",
  "timestamp": "{{time}}",
  "secret": "你在 bot 后台设的 webhook secret"
}
```

**必须改的两处**：

1. `"strategy_id": "macd_eth_long"` —— 改成你在 bot 后台 /strategies 创建的那个 ID
2. `"secret": "..."` —— 改成你在 /settings 里设的 Webhook Secret

**不要动**的：`{{ticker}}`、`{{strategy.order.action}}`、`{{strategy.position_size}}`、`{{close}}`、`{{time}}` 这些是 TradingView 的占位符，触发时自动替换。

> 💡 `timestamp` 必须用引号包起来：TradingView 的 `{{time}}` 输出 ISO 8601 字符串（`2026-05-06T15:29:00Z`）而不是数字，不加引号会破坏 JSON 语法。bot 同时支持 ISO 字符串和 Unix 毫秒数字两种格式。

> 💡 `signal` + `position_size` 配合使用：TradingView 策略告警里 `{{strategy.order.action}}` 只输出 `buy`/`sell`，无法区分「开多」「平空」。bot 用 `{{strategy.position_size}}`（下单后仓位）消歧：
>
> | signal | position_size | bot 解读 |
> |--------|---------------|---------|
> | buy    | > 0           | 开多（long）|
> | buy    | == 0          | 平空（exit_short）|
> | sell   | < 0           | 开空（short）|
> | sell   | == 0          | 平多（exit_long）|
>
> 如果你用 curl 手动测试或者 Pine 自定义 alert，也可以直接发 `"signal": "long"` / `"short"` / `"exit_long"` / `"exit_short"`，这种情况下 `position_size` 字段可以省略。

#### 3.6 创建告警

点对话框右下角 **「创建」** 按钮。

#### 3.7 测试触发

回到 TradingView 主界面 → 右侧栏 **🔔 告警面板** → 找到刚创建的告警 → 点 **⋯ 三个点 → 「触发」**（有的版本叫「Test」或「Send notification」）。

立刻去 bot 后台 http://localhost:8080/signals 刷新——应该看到一条新记录：

| 时间 | 策略 | 方向 | decision |
|------|-----|------|---------|
| 刚刚 | macd_eth_long | long/short | accepted ✅ |

如果 decision 是其他值，参考下面的 [常见错误对照表](#常见错误对照表)。

---

## 运行模式

| 模式 | 是否真下单 | 用途 |
|------|----------|------|
| `testnet` | ✅ 但是测试币 | 币安测试网，验证下单逻辑（推荐先跑这个） |
| `live` | ✅ 真钱 | 生产 |

切换模式：改 `.env` 的 `BOT_MODE` → 重启 bot。两种模式都需要 Binance API key/secret（测试网与实盘的 key 是分开签发的，不能混用）。

**testnet 怎么玩**（强烈推荐先在这里跑一遍）：

1. **注册测试网账号**：https://testnet.binancefuture.com/
   - 第一次访问会自动赠送约 100,000 测试 USDT
   - 没有 KYC、邮箱验证等麻烦
2. **拿测试网 API Key**：
   - 登录后右上角点 **「API Key」**
   - 直接「Generate HMAC_SHA256 Key」即可
   - 复制 API Key 和 Secret Key（**Secret 只显示一次**！）
3. **填到 bot 后台**：http://localhost:8080/settings → **币安 API** 区块 → 填 testnet 的 key/secret → 保存
4. **切换模式**：`.env` 改 `BOT_MODE=testnet` → 重启 bot
5. **测试余额不够了**：去 https://testnet.binancefuture.com/en/balance/funds 点「Free $1000」按钮领取

**live 上线 checklist**：

- [ ] 已经在 testnet 跑通完整流程（开仓 + 止损触发 + 平仓）
- [ ] **币安实盘账号已开通合约**：https://www.binance.com/zh-CN/futures （第一次需通过简单测试题）
- [ ] **创建 API Key**：https://www.binance.com/zh-CN/my/settings/api-management
  - ✅ 勾「Enable Futures」（合约交易，必须）
  - ❌ **不勾「Enable Withdrawals」**（防止资金被卷走）
  - ❌ 不勾「Enable Spot & Margin Trading」（不用）
- [ ] **API Key 绑定 IP 白名单**（页面上「Restrict access to trusted IPs only」），填你 VPS 公网 IP
- [ ] bot 后台 /settings 填入 live key/secret，重启 bot
- [ ] 用最小 size_usdc（比如 10 USDC）跑一笔实盘验证
- [ ] **配好 Telegram/飞书告警**（/settings 里），确认能收到下单通知

---

## 实盘（live）运行手册

> **实盘 = 真钱**。代码 bug 不会扣 testnet 的虚拟币，只会扣你账户里的 USDT。所以这一节请逐条做完,不要跳。

### 阶段 1：上线前的硬门槛

#### 1.1 testnet 实测合格证

在切换到 live 之前,你应该已经在 testnet **稳定运行至少 2 周**,并验证过下面所有场景:

- [ ] 开仓 + 平仓信号都能正常触发,Lark/Telegram 通知中文+完整数据
- [ ] 反手信号(close-and-open)发出 2 条通知(平仓 + 开仓)且数字对得上
- [ ] 止损实际触发过至少一次(或者你手工测试过强制移近止损价)
- [ ] 重启 bot,启动恢复无异常告警(或者主动制造异常验证恢复流程)
- [ ] `/stats` 页面累计 PnL 与币安账户余额变动对得上(差异只在 funding fee)
- [ ] 风控触发过至少一次(故意压杠杆或单日亏损,看是否拒绝新开仓)
- [ ] cloudflared / 反代 / VPS 都跑过 24h+ 没掉

如果有一项没验过,**回 testnet 继续跑**。

#### 1.2 心理 + 资金门槛

- 你能承受 **100% 亏损这笔钱**(币安期货可以爆仓归零)
- 入金金额是你**真愿意亏掉**的,而不是"我想赚的"。建议第一次 live 入金 ≤ 月薪
- 你看得懂 Lark/Telegram 的中文通知,知道每条数字什么意思
- 你知道怎么紧急 disarm(后面有快捷方式)

### 阶段 2:实盘账户与 API Key

#### 2.1 币安实盘账户

```
1. https://www.binance.com/zh-CN/futures
   → 第一次访问会有「开通合约」流程,做几道选择题(都是基础知识)
2. 充值 USDT 到合约钱包
   → 现货钱包不能直接下合约,要先「资金划转」到 USDⓂ 合约钱包
3. 选择持仓模式:**单向持仓**(One-way Mode)
   → 设置 → 偏好设置 → 持仓模式 → 单向
   → bot 当前不支持 Hedge mode(双向持仓)
```

#### 2.2 创建 live API Key

[https://www.binance.com/zh-CN/my/settings/api-management](https://www.binance.com/zh-CN/my/settings/api-management)

**权限**(必看):

| 权限 | 设置 | 原因 |
|---|---|---|
| Enable Reading | ✅ 必勾 | 读取仓位/订单/income |
| **Enable Futures** | ✅ **必勾** | 下合约单核心权限 |
| Enable Spot & Margin Trading | ❌ 不勾 | bot 只用合约 |
| **Enable Withdrawals** | ❌ **绝对不勾** | 防 API 被泄露后资金被盗 |
| Enable Internal Transfer | ❌ 不勾 | 同上 |
| Enable Universal Transfer | ❌ 不勾 | 同上 |

#### 2.3 IP 白名单(live 强制要求)

> ⚠️ **不绑 IP 白名单的 live API Key 是裸的**。币安要求合约 API Key 必须绑 IP,否则报 `-2015 Invalid API-key`。

```
1. API Management 页面 → 你刚建的 key → 「Edit Restrictions」
2. 选 「Restrict access to trusted IPs only (Recommended)」
3. 填你 VPS 的公网 IP(`curl -4 ifconfig.me` 在 VPS 上查)
4. 保存
```

如果你的 VPS 是动态 IP,买个固定 IP(主流云厂商 ~$3-5/月)。

#### 2.4 账户初始设置(币安 UI 上)

- **杠杆模式**:逐仓(Isolated) 或 全仓(Cross),按你策略需求选
  - bot 不主动改杠杆,沿用你账户上每个 symbol 的现有设置
  - 如果 size_usdc=100 leverage=5,下单时 bot 会按 5x 计算 qty,但 Binance 实际杠杆是你账户上 ETHUSDT 设置的那个值——**两边要对上**
- **手续费**:开 BNB 抵扣 + VIP 等级提升 = 手续费降一半,长期省不少

### 阶段 3:bot 配置(转 live)

#### 3.1 风控参数(/settings)

| 参数 | testnet 默认 | live 推荐 | 备注 |
|---|---|---|---|
| `max_total_leverage` | 3.0 | **2.0**(初期) | 等跑顺了再放到 3.0+ |
| `max_daily_loss_usdc` | 500 | **你能承受的最大单日亏损** | 触发后当日只接平仓 |
| `reconciler_interval_seconds` | 30 | **60** | live 更省 API 配额 |
| `binance_recv_window_ms` | 5000 | 5000 | 正常 |
| `binance_order_timeout_ms` | 3000 | **5000** | live 网络抖动余量大点 |

每个策略的 `size_usdc`(单次开仓金额):**live 第一周用 10-20 USDC**,确认全链路无问题再放大。

#### 3.2 通知必须配上

- Lark webhook URL 配好,enabled=true
- Telegram bot token + chat_id 配好,enabled=true
- 这两个是你 live 出问题的**唯一**及时报警渠道。bot 异常 → 飞书 [CRITICAL] 告警 → 你立刻看到 → 决定是否手工 disarm

#### 3.3 切换 BOT_MODE

```bash
# .env
BOT_MODE=live           # 不再是 testnet

# 重启 bot
sudo systemctl restart tvbot   # 或 docker compose restart bot
```

启动日志里应该看到:
```
================================
        tvbot starting
  mode: live
  ⚠️  LIVE MODE — real money at stake
  armed: false (run /system/arm to enable)
================================
```

**没看到 ⚠️ 警告说明你切换没生效**,检查环境变量。

### 阶段 4:首单验证(必做)

不要直接跑 4 个策略 4 个币种全开。第一周走这个流程:

```
1. 只 enable 1 个策略,size_usdc=10
2. /system/arm 启用交易
3. 等 TradingView 触发第一条信号
4. 立即检查:
   - Lark/Telegram 收到中文通知,价格/数量/方向都对 ✓
   - /positions 页面有这个仓位,跟币安 UI 上对得上 ✓
   - 币安 UI 上看到 stop / backup_stop / take_profit 三个保护单都挂着 ✓
   - DB 里 virtual_positions 有对应行 ✓
5. 等平仓:
   - 通知里盈亏数字 ✓
   - /stats 累计 PnL 加上了这一笔 ✓
   - 币安账户余额变化 = /stats 显示的 PnL - funding ✓
   - position_history 表有这一行 ✓
6. 这一笔全对得上,再放开第 2 个策略
```

任何一步对不上,**立即 disarm + 排查**,不要继续。

### 阶段 5:持续监控

#### 5.1 每日检查项

- [ ] /stats 累计 PnL = 币安账户余额累计变化(±funding)
- [ ] /positions 上每条仓位都跟币安 UI 一致(数量/方向)
- [ ] Lark/Telegram 没有 `[CRITICAL]` 级别的告警(有的话立刻处理)
- [ ] tvbot.log 没有连续重复的 ERROR(偶尔一两条网络抖动正常)

#### 5.2 长期检查项(每周)

- VPS 资源:CPU/内存/磁盘空间(`df -h` postgres data 不要 >80%)
- cloudflared 进程在跑(`pgrep cloudflared`),日志没大量 4xx/5xx
- API Key 权限没被改动(币安偶尔会因为风控原因关停权限)
- TradingView 警报没过期(免费版 alert 60 天到期,Pro 不限)

### 阶段 6:应急处理

#### 6.1 紧急停止接单(disarm)

```bash
# 方式 1:浏览器最快
# 打开 http://你的域名/system → 点「停止交易」

# 方式 2:命令行(需要先登录拿 cookie)
curl -b "session=xxx" -X POST http://localhost:8080/system/disarm
```

disarm 后 bot **不再接受新信号**,但**不会平掉现有仓位**——保护单(止损/止盈)依然挂在 Binance 上。

#### 6.2 紧急平掉所有仓位

如果你判断需要立即清仓(比如发现 bot 行为异常),**直接去币安 UI 手动平**——不要等 bot 处理。然后:

```bash
# 重启 bot,startup recovery 会自动同步 DB 状态
sudo systemctl restart tvbot
# 看到 "Position auto-closed during recovery" 通知就对了
```

#### 6.3 怀疑 bot 行为不对(下错单 / 数量不对)

```bash
# 1) 立即 disarm(上面)
# 2) 截图币安 UI 上的实际仓位
# 3) 截图 /positions 页面上的 bot 视角仓位
# 4) 抓最近的 webhook 日志:
grep -E "/webhook/tv|action_taken|decision" /tmp/tvbot.log | tail -50
# 5) 对照 strategy_id + symbol + qty,找出哪一步不对
```

不要在 bot 异常时**重启**——重启可能让 startup recovery 把不一致的仓位自动 close,**丢掉证据**。先 disarm + 抓日志 + 手工平仓,再排查。

#### 6.4 API Key 泄露

立刻去币安 API 管理页面**删除**(不是 revoke,直接 Delete)那把 key,然后:

```bash
# 创建新 key,重新绑 IP 白名单
# 在 /settings 替换 API Key/Secret
# 重启 bot
```

因为不勾 Withdrawals,泄露的 key 最多被人下点垃圾单恶心你,不会卷款。但还是越快换越好。

### 阶段 7:live vs testnet 真实差异(踩坑表)

| 现象 | testnet | live | 应对 |
|---|---|---|---|
| 滑点 | 经常几十 USDT 离谱滑点 | 通常 ≤ 0.1% | 信号价 vs 成交价对比看通知里的"滑点"字段 |
| 订单立即成交率 | 高(撮合稀疏匹配宽) | 取决于挂单深度 | live 用 MARKET 订单成交,不存在排队问题 |
| Funding fee | 经常 0 或异常 | 真实,每 8h 结算 | /stats 里 funding 单独显示,不计入 position_history.pnl |
| `min_notional` 拒绝 | 几乎不会 | BTCUSDT 等大币种最小 ~$5 | size_usdc 太小会被拒,看通知里的"开仓失败"原因 |
| `LOT_SIZE` 拒绝 | 宽松 | 严格按 stepSize | bot 会自动 floor,但精度太小可能 qty=0 → 拒绝 |
| API rate limit | ~2400 req/min | 真实限速 | reconciler interval 拉到 60s,多策略不要全 30s |
| 网络偶发 5xx | 少 | 偶尔 | reconciler 自动重试,通知里看到 1-2 条 warn 不用管,连续 10+ 条要查 |
| `-1021 timestamp out of recv window` | 少 | 系统时间漂移会触发 | VPS 装 ntp,`timedatectl` 确认时间同步 |

### 阶段 8:常见踩坑

#### 坑 1:策略配置时 leverage 跟币安账户对不上

bot 用 `leverage` 计算 qty,但实际下单时 Binance 用账户上**该 symbol 的杠杆设置**。两边不一致 → 实际持仓金额跟你预期差几倍。

**修复**:在 Binance UI 上,每个你要交易的 symbol(ETHUSDT/BTCUSDT/...),点开 symbol → 杠杆设置,改成跟 bot 策略里的 `leverage` 字段一致。

#### 坑 2:Hedge mode 模式开启了

bot 不支持 hedge mode。如果 Binance 账户开了 hedge,bot 下的 SHORT 信号会变成 "open short hedge",和 LONG 不会自动对冲。

**修复**:Binance UI → 偏好设置 → 持仓模式 → 单向(One-way)。

#### 坑 3:webhook URL 是临时 trycloudflare 域名

`*.trycloudflare.com` 每次重启 cloudflared 域名都换,live 千万别用临时 tunnel——某天云服务器重启,你的 TradingView 信号就**永远进不来**了。

**修复**:用绑你自己域名的 named tunnel(本文档「部署到生产」章节有详细命令)。

#### 坑 4:重启后忘记 arm

bot 启动后**强制 disarm**。重启后(包括 OOM、systemd restart、机器重启)如果你不手动 arm,信号过来全部拒绝。

**修复**:启动恢复完成后,Lark/Telegram 会推送 "system disarmed" 通知。看到了**记得手动 arm**。或者写个 systemd ExecStartPost 自动 arm(但慎用——bot 可能因为 startup recovery 异常而不该 arm)。

#### 坑 5:停止 cloudflared 但忘记取消 TradingView 警报

cloudflared 停了,但你的 TradingView 警报还在每 3 分钟尝试 POST。TradingView 服务端会因为多次失败**自动停用**这个 alert(免费版),你以后也收不到信号。

**修复**:停 bot 之前先去 TradingView **关闭警报**,或者保持 cloudflared 一直跑。

---

## 运维操作

### 启动/停止交易

后台 http://localhost:8080/system 上有大按钮，或：

```bash
# 启动
curl -X POST http://localhost:8080/system/arm   # 需要 cookie

# 停止（紧急）
curl -X POST http://localhost:8080/system/disarm
```

### 日亏熔断

当日累计亏损（`system_state.daily_pnl_usdc`）跌破 `-max_daily_loss_usdc` 时：
- ❌ 所有**新开仓**信号被拒（decision=risk_denied）
- ✅ **平仓**信号正常处理（避免熔断卡死现有持仓）
- 每天 UTC 0 点自动重置

手动重置：`/system` 页面有按钮（仅在熔断触发时显示）。

### 查看实时日志

```bash
# bot 在前台跑：直接看终端
# bot 在后台跑：
tail -f /tmp/tvbot.log

# 关键事件 grep
grep -E '"event":"order_filled|stop_loss_triggered|breaker_tripped"' /tmp/tvbot.log
```

### 后台对账

每 30 秒（可在 /settings 改）一次后台 goroutine 检查未结订单，从交易所同步真实状态。**启动时**还会跑一次完整恢复（防止 bot 崩溃期间漏掉的成交）。

---

## 配置参考

> 大部分配置都可以在 **bot 后台** http://localhost:8080/settings 修改。env 和 yaml 只是**首次启动**的初始值，之后 DB 是事实源。

### env 变量（`.env`）

这些**只能在 env 里**（不能通过后台改）：

| 变量 | 必须？ | 说明 |
|------|------|------|
| `BOT_MODE` | ✅ | `testnet` / `live` |
| `DATABASE_URL` | ✅ | Postgres 连接串 |
| `SESSION_SECRET` | ✅ | 浏览器 cookie 加密，至少 32 字节 |
| `HTTP_LISTEN` | | 默认 `0.0.0.0:8080` |
| `LOG_LEVEL` | | `debug` / `info` / `warn` / `error` |
| `WEBHOOK_SECRET` | 首次 | 仅作为首次启动 bootstrap 到 DB；之后改后台为准 |
| `BINANCE_API_KEY` | 首次 | 同上 |
| `BINANCE_API_SECRET` | 首次 | 同上 |

### Web 后台 `/settings` 可改（运行时）

| 配置 | 是否实时生效 |
|------|---------|
| 风控阈值（max_total_leverage、max_daily_loss_usdc） | ✅ 实时 |
| Webhook Secret | ✅ 实时 |
| IP 白名单 | ✅ 实时 |
| 飞书 webhook URL | ❌ 需重启 bot |
| Telegram bot token / chat ID | ❌ 需重启 bot |
| Binance API Key/Secret | ❌ 需重启 bot |
| Reconciler 周期（秒） | ❌ 需重启 bot |
| Binance recv_window / 下单超时 | ❌ 需重启 bot |

---

## 部署到生产

### 方案 1：单 VPS + Cloudflare Tunnel（推荐）

```bash
# 在 VPS 上
git clone git@github.com:alen1668/AutoTradeX.git tvbot
cd tvbot
cp .env.example .env && $EDITOR .env

# docker-compose 把 bot + postgres 一起起
docker compose up -d

# 装 cloudflared 并配置永久 tunnel（绑你的域名）
brew install cloudflared
cloudflared tunnel login
cloudflared tunnel create tvbot
cloudflared tunnel route dns tvbot bot.yourdomain.com
cloudflared tunnel run tvbot
```

之后 TradingView 填 `https://bot.yourdomain.com/webhook/tv`。

### 方案 2：Caddy 反代 + Let's Encrypt

```caddyfile
bot.yourdomain.com {
    reverse_proxy localhost:8080
}
```

Caddy 自动申请证书。

### 方案 3：systemd（裸金属）

```bash
make build
sudo cp bin/tvbot /usr/local/bin/tvbot
sudo cp .env /etc/tvbot.env

cat <<EOF | sudo tee /etc/systemd/system/tvbot.service
[Unit]
Description=tvbot
After=network.target postgresql.service

[Service]
EnvironmentFile=/etc/tvbot.env
ExecStart=/usr/local/bin/tvbot
Restart=on-failure
User=tvbot

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl enable --now tvbot
```

---

## 测试

```bash
# 单元测试（无外部依赖）
go test -race ./...

# 集成测试（自动起 dockertest postgres）
go test -tags=integration -race ./...

# 币安 testnet 实测（需 API key）
BINANCE_TESTNET_KEY=xxx BINANCE_TESTNET_SECRET=yyy \
  go test -tags=integration_binance ./internal/infrastructure/binance/...
```

CI 配置在 `.github/workflows/ci.yml`，每次 push 自动跑。

---

## 架构

```
TradingView ─webhook─► /webhook/tv ─► [parse → idempotent → risk → decide → trade → notify]
                                                                          │
                          ┌─ Binance Futures API ◄─── Trader (DryRun/Live)│
                          │
PostgreSQL ◄── repos ◄── application/{ingest,trade,reconcile}             │
                                                                          │
                                       Notifier (Feishu/Telegram) ◄───────┘
```

| 包路径 | 职责 |
|--------|------|
| `cmd/tvbot` | 进程入口，装配依赖 |
| `internal/application/ingest` | 信号摄入完整管道 |
| `internal/application/trade` | 开仓 / 平仓 / 双止损挂单 |
| `internal/application/reconcile` | 订单对账 + 启动恢复 |
| `internal/risk` | 4 个风控规则 |
| `internal/store` | Postgres 数据访问层 |
| `internal/web` | HTTP 入口、admin UI、middleware |
| `internal/infrastructure/binance` | 币安 SDK 封装（live + testnet） |
| `internal/notify` | 飞书 + Telegram |
| `internal/idempotency` | LRU + DB 双层去重 |

---

## 常见问题 FAQ

### Q: TradingView 没付费 webhook 能用吗？

A: 不能，免费账号没 webhook 功能。但你可以**用 curl 手动测试整个链路**：

```bash
curl -X POST http://localhost:8080/webhook/tv \
  -H 'Content-Type: application/json' \
  -d '{"strategy_id":"macd_eth_long","symbol":"ETHUSDC","signal":"Long","price":"2300","timestamp":1714900000000,"secret":"你的-webhook-secret"}'
```

返回 `{"decision":"accepted","action":"open_long"}` 就说明 bot 端没问题。

### Q: 启动后 webhook 收到都是 `decision: "duplicate"` 怎么办？

A: TradingView 重发同一信号时，bot 通过 `(strategy_id, timestamp)` 去重。要么改 timestamp、要么这本来就是重复信号。看 /signals 页对应行的 reason。

### Q: `decision: "disarmed"` 是什么意思？

A: 系统是「停止交易」状态。去 /system 页面点「启动交易」按钮。每次重启都会自动 disarm，这是安全设计。

### Q: `decision: "risk_denied"` 怎么看具体哪条规则拒了？

A: /signals 页面那条信号的 `reason` 字段会写：
- `notional ... > strategy.max_open_usdc` → 单策略上限
- `leverage ... > max_total_leverage` → 总杠杆
- `daily loss ... > max` → 日亏熔断
- `ip ... not in whitelist` → IP 白名单（注意 tunnel 转发的源 IP 问题）

### 常见错误对照表

| 现象 | 原因 | 解决 |
|-----|------|------|
| TV 触发后 bot 没收到任何东西 | tunnel 没起 / URL 错 | 验证 `curl https://your-tunnel/healthz` 返回 ok |
| 收到但 `decision=invalid, reason=secret mismatch` | TV 那边的 secret 和 bot 的不一致 | 检查 /settings 的 Webhook Secret 和 TV alert 消息里的 `secret` 字段 |
| 收到但 `decision=invalid, reason=signal required` | TV 消息不是 JSON 格式 | 检查 alert message 是否原样复制了上面的 JSON 模板 |
| 收到但 `decision=risk_denied, reason=ip ... not in whitelist` | tunnel 转发的源 IP 不在白名单 | /settings 清空白名单，或加上 cloudflare 出口 IP |
| 收到但 `decision=disarmed` | 系统未启动 | /system 点「启动交易」 |
| 信号收到但 testnet 没下单 | 一般是 API key 没勾「Enable Futures」或 testnet key 配错 | /settings 重新粘贴，注意 testnet key 与实盘 key 是分开签发的 |

### Q: 我能多人共用一个 bot 实例吗？

A: 现在 MVP 设计是单用户单账户。多人多 API Key 隔离没做（spec §1.3 列入 V2）。

### Q: 怎么备份数据？

A: 备份 PostgreSQL：

```bash
docker exec crypto-postgres-1 pg_dump -U tvbot tvbot > backup.sql
```

恢复：

```bash
cat backup.sql | docker exec -i crypto-postgres-1 psql -U tvbot -d tvbot
```

### Q: 我要上线生产前还有什么坑？

参考运行模式章节的 **live 上线 checklist**。最关键的几条：

1. **必须先 testnet 跑通完整流程**
2. **API Key 不要授予提币权限**（被盗也只能交易，资金安全）
3. **API Key 设 IP 白名单**（VPS 公网 IP）
4. **第一笔单用最小 size_usdc**（比如 10 USDC）

---

## 许可证

MIT — 详见 [LICENSE](LICENSE)。

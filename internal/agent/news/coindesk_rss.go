package news

// CoinDeskRSSFetcher 是 RSSFetcher 的向后兼容别名,旧代码不需要改。
type CoinDeskRSSFetcher = RSSFetcher

// NewCoinDeskRSSFetcher 旧入口,等价于 NewRSSFetcher("coindesk", url, "CoinDesk")。
func NewCoinDeskRSSFetcher(url string) *RSSFetcher {
	return NewRSSFetcher("coindesk", url, "CoinDesk")
}

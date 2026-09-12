package wiring

import (
	"database/sql"
	"sync"

	"github.com/context-flow/ic/internal/provider"
)

// PricingTable 是价格表（整数微元，INV-9）。
//
// 为什么要有一张显式的价格表：cost 是用户最敏感的字段之一。
// 如果成本靠「上游返回值反推」或「猜一个系数」，一旦上游不返回用量，
// 账单就会静默变成 0，用户会以为「免费」。这里的原则是：
//   - 未配置价格的模型返回零值，并在 UI 明确标注「未配置价格」；
//   - 价格可配置，不做隐性默认。
type PricingTable struct {
	db *sql.DB

	mu    sync.RWMutex
	cache map[string]provider.Pricing
}

// NewPricingTable 构造价格表。
func NewPricingTable(db *sql.DB) *PricingTable {
	return &PricingTable{db: db, cache: map[string]provider.Pricing{}}
}

// Pricing 实现 provider.CredentialResolver 需要的查询函数。
func (p *PricingTable) Pricing(providerID, model string) provider.Pricing {
	key := providerID + "|" + model
	p.mu.RLock()
	cached, ok := p.cache[key]
	p.mu.RUnlock()
	if ok {
		return cached
	}
	return provider.Pricing{}
}

// Set 更新某模型的价格（配置中心写入口）。
func (p *PricingTable) Set(providerID, model string, pricing provider.Pricing) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[providerID+"|"+model] = pricing
}

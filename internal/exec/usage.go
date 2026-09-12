package exec

import (
	"github.com/context-flow/ic/internal/provider"
)

// PricingTable 是价格表：按 provider+model 覆盖，未命中时用 default。
// 单位一律为「微元」（1e-6 货币单位），避免浮点误差（INV-9）。
type PricingTable struct {
	Default provider.Pricing
	ByModel map[string]provider.Pricing
}

// PricingFor 返回某模型的价格。
func (t PricingTable) PricingFor(providerID, model string) provider.Pricing {
	if t.ByModel != nil {
		if p, ok := t.ByModel[providerID+"/"+model]; ok {
			return p
		}
		if p, ok := t.ByModel[model]; ok {
			return p
		}
	}
	return t.Default
}

// ComputeCost 用整数运算计算成本（INV-9：聚合后与逐项求和无误差）。
func ComputeCost(p provider.Pricing, u provider.Usage) int64 {
	const perMillion = 1_000_000
	var total int64
	if u.TextTokensIn > 0 && p.TextInputPerMTok > 0 {
		total += u.TextTokensIn * p.TextInputPerMTok / perMillion
	}
	if u.TextTokensOut > 0 && p.TextOutputPerMTok > 0 {
		total += u.TextTokensOut * p.TextOutputPerMTok / perMillion
	}
	if u.Images > 0 && p.ImagePerUnit > 0 {
		total += u.Images * p.ImagePerUnit
	}
	if u.VideoMillis > 0 && p.VideoPerSecond > 0 {
		total += u.VideoMillis * p.VideoPerSecond / 1000
	}
	if u.AudioMillis > 0 && p.AudioPerSecond > 0 {
		total += u.AudioMillis * p.AudioPerSecond / 1000
	}
	return total
}

// ApplyCost 计算并写入成本字段。
func ApplyCost(p provider.Pricing, u provider.Usage) provider.Usage {
	u.CostMicros = ComputeCost(p, u)
	return u
}

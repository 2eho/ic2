package provider

import "context"

// 这里集中别名，避免在 classify 逻辑里引入 context 依赖扩散。
var (
	context_Canceled         = context.Canceled
	context_DeadlineExceeded = context.DeadlineExceeded
)

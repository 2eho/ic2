package graph

// 本文件是**边界常量的唯一真源**（见 docs/design/13 §6：禁止文档与代码两处维护）。
// scripts/check-boundaries.mjs 会校验 docs 与这里的数值一致。

const (
	// MaxOpBatch 单次提交的 op 数上限。
	MaxOpBatch = 1000
	// MaxRequestBytes 单请求体字节上限（1MB）。
	MaxRequestBytes = 1 << 20
	// MaxNodesPerCanvas 单画布节点硬上限。
	MaxNodesPerCanvas = 200_000
	// MaxTitleLen 节点标题长度上限（字符）。
	MaxTitleLen = 200
	// MaxPromptBytes 提示词/文本内容字节上限（32KB）。
	MaxPromptBytes = 32 * 1024
	// MaxVariantsPerNode 单节点结果变体上限。
	//
	// 与 outputCount 共用同一上限：一次生成最多产出 N 个变体，
	// 回写时也只能写入 N 个。两处用不同数字会导致「能生成但不能回写」。
	MaxVariantsPerNode = 15
	// MinVariantsPerNode 单节点结果变体下限（至少产出 1 个）。
	MinVariantsPerNode = 1
)

// 几何边界。
const (
	CoordMin = -1e7
	CoordMax = 1e7
	SizeMin  = 16.0
	SizeMax  = 20000.0
	ZoomMin  = 0.05
	ZoomMax  = 5.0
)

// 分组嵌套深度上限（防无限递归）。
const MaxGroupDepth = 8

// MaxEdgesPerCanvas 边数上限。
const MaxEdgesPerCanvas = 400_000

// IDRe 说明：ID 校验见 id.go，格式 ^[A-Za-z0-9_-]{1,64}$。
const MaxIDLen = 64

// 节点扩展元数据（Node.Meta）的边界。
//
// Meta 是「明确不参与执行」的扩展位，但它会进画布文档、进导出包、
// 进每一次快照传输。没有上限时它会变成隐形数据库：
// 用户往里塞图片 base64，之后导出/同步/快照全部变慢，而没人知道为什么。
const (
	// MaxMetaKeys 单节点 meta 键数量上限。
	MaxMetaKeys = 64
	// MaxMetaKeyLen 单个 meta 键名长度上限。
	MaxMetaKeyLen = 128
)

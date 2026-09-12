# 11 · 边界推演与对抗（死角清单）

> 目的：**在设计阶段把系统打死**，而不是上线后被打死。
> 方法：先确定系统的不变量（invariants），再用「对抗者视角」逐层攻击；
> 每个死角必须给出「机制性修复」而不是「小心一点」。
>
> 标记：`P0` 会导致数据损坏/资金损失/不可恢复；`P1` 功能不可用或体验崩坏；`P2` 长期腐蚀。
> 每条格式：**攻击 → 后果 → 根因 → 机制性修复 → 验收用例**。

## 1. 不变量（Invariants）

系统任何时刻都必须满足以下命题。违反即视为 P0 缺陷，无论表现多轻微。

| ID | 不变量 |
| --- | --- |
| INV-1 | 画布文档任一份已落库的 op 日志前缀，重放后必须得到与当时权威快照一致的文档 |
| INV-2 | 同一 `Idempotency-Key` 的写操作至多生效一次 |
| INV-3 | 同一 `provider_credential` 的任一 `request_id`，在外部 Provider 侧至多产生一次计费 |
| INV-4 | 任一 `asset_refs` 记录被删除后，其指向的 Blob 在保留窗口内仍可读 |
| INV-5 | 凭据明文**永不**出现在任何 HTTP 响应、日志、错误信息、前端内存 |
| INV-6 | 插件代码**永不**获得宿主 origin 的 DOM/Cookie/localStorage 访问权 |
| INV-5b | 用户上传的资产被导航访问时，**永不**在应用 origin 下作为可执行文档运行（SVG/HTML/XML 一律降级为不可执行类型 + `nosniff`） |
| INV-6b | 插件可调用的方法名必须是**自有属性**：`__proto__` / `constructor` 等原型链键一律拒绝（`in` 判定会误放行） |
| INV-7 | `agent_items(turn_id, item_id)` 唯一；实时事件与历史快照合并后不重复、不丢失 |
| INV-8 | 任一写操作的 `actor_id` 可追溯，且不等于「无法归属」 |
| INV-9 | 金额/成本用整数微元表示，聚合后与逐项求和不产生浮点误差 |
| INV-10 | 删除工作区/项目后，其他工作区不可再读到其任何数据（含通过 ID 猜测） |
| INV-11 | 用户提供的自定义脚本**永不**获得宿主能力：无动态求值、无循环、无原型链、无宿主全局；其产出的请求只能落在渠道 `baseUrl` 之下，且不能设置由平台管理的头部 |
| INV-12 | 直连降级通道**默认关闭**，开启必须显式确认风险且地址为回环；生效范围由用户逐项勾选，不由服务端默认扩大 |

## 2. 边界推演（Boundary）

### 2.1 数值边界

| 场景 | 极限输入 | 风险 | 修复 |
| --- | --- | --- | --- |
| 节点坐标 | `x = 1e15`、负数十亿、`NaN`、`Infinity` | JSON 序列化丢精度、渲染 OOM、命中测试错乱 | op 校验：坐标限 `±1e7`，非有限数直接 422；DB 用 `double precision` 且入库前 `math.IsNaN` 拒绝 |
| 节点尺寸 | 宽高 0 或 1e9 | 布局除零、`fitNodeSize` 出 NaN | 尺寸限 `[16, 20000]`，服务端强制 |
| 缩放 k | 0、负数、1e6 | 视口矩阵退化；栅格计算溢出 | 限 `[0.05, 5]`（与原项目一致），前端与服务端双校验 |
| 批量节点数 | 一次 op 提交 100 万个 add_node | 请求体 OOM、SSE 广播风暴、前端冻结 | 单次 op 数 ≤ 1000，单请求字节 ≤ 1MB，超限 413 |
| 画布节点总量 | 100 万节点 | 快照 JSON 过大，内存爆 | 文档分片（`canvas_docs` 存增量 + 定期压实），编辑器侧走视口裁剪；硬上限可配置并明确报错 |
| 图片张数 count | 0 / 负数 / 1e9 / `"3; DROP"` | 费用失控、请求被中转站拒绝 | count 限 `[1, 15]`（对齐原项目 15 与工作台 10），服务端 schema 强校验 |
| 视频时长 | 3s / 31s / `"abc"` | 上游 400，费用已扣 | 限 `[4, 30]` 整数（原项目一致），越界在提交前拒绝 |
| 分辨率 | 0x0、99999x99999、比例 100:1 | 上游拒绝或产生巨额费用 | 对齐原项目约束：步长 16、单边 ≤3840、像素 `[655360, 8294400]`、比例 ≤3 |
| 放大目标边长 | 99999 | 浏览器 OOM 崩溃 | 上限 4096（原项目一致），超出提示已达上限 |
| 切图行列 | 0 行、1e6 行 | 死循环/节点爆炸 | 行列限 `[1, 50]`，且 `行列积 ≤ 200` |
| 计时/时长 | 生成耗时 10 小时、负数 | 展示 `-1s`、超时判断失效 | 统一 `time.Duration`；前端格式化为 `0s` 兜底 |
| 成本 | `0.1 + 0.2` 类浮点 | 对账误差累积 | INV-9：整数微元 + 整数乘加 |
| 分页 limit | 0 / 负数 / 1e9 | 全表扫描 | limit 限 `[1, 200]`，默认 20 |

### 2.2 时间边界

| 场景 | 攻击 | 后果 | 修复 |
| --- | --- | --- | --- |
| 时钟回拨 | 客户端提交 `updatedAt` 在未来 | 同步「取最新」策略永远偏向该端，其他端改动被吞 | **服务端时间权威**：`UpdatedAt` 由服务端写；客户端时间只作为「本地乐观」提示 |
| 客户端任意时间戳 | 伪造 `deletedAt` | 墓碑压制后续合法修改（原项目 WebDAV 合并算法存在此面） | 墓碑带服务端序号 + 签名；合并按服务端序而非时间 |
| 长时任务跨重启 | 视频任务运行中宕机 | 任务丢失，用户重复付费 | Attempt 持久化 `remote_task_id` + `provider`，重启后 rehydrate 继续轮询（对齐原项目能力） |
| 任务超时 | Provider 永不返回 | Worker 占满 | 每 Step 有 `deadline`，超时按 transient 重试或判失败；Worker 有租约心跳 |
| 令牌过期 | SSE 连接长于 session 有效期 | 中途断流 | SSE 在连接内不重新鉴权，但连接有最大寿命（默认 30min）到点发 `reconnect` 事件让前端重连 |
| 时区 | 日限额按客户端时区算 | 用户可绕过限额 | 限额按工作区配置时区（默认 UTC）在服务端计算 |
| 夏令时 | 时区切换当天的日额度 | 重复或跳过 | 用 `time.LoadLocation` + 日期边界计算，不用固定 24h 偏移 |
| 系统休眠 | 笔记本合盖 8 小时后唤醒 | 心跳超时、任务被判死 | 任务所有权由服务端租约决定，客户端断线不影响执行 |

### 2.3 规模边界

| 场景 | 极限 | 风险 | 修复 |
| --- | --- | --- | --- |
| 5000 节点同屏 | 视口操作 | React 重渲染风暴 | 内核与 React 解耦 + 视口裁剪 + 降级占位（见 04-frontend） |
| 单画布 1000 op/s | 多人同时编辑 | op 日志膨胀、SSE 洪泛 | op 批量合并（rAF）+ 服务端按订阅者背压；日志按周分区并保留 N 个快照 |
| 资产 100 万 | 列表与 GC | 全表扫描、GC 卡死 | 游标分页 + `asset_refs` 反向索引；GC 分批、有水位线 |
| SSE 1 万连接 | 单实例 | FD 耗尽 | 连接数上限 + 超限快速 503；集群模式经 Redis Pub/Sub 扇出 |
| 大文件上传 | 20GB 视频 | 内存爆 | 分片上传 + 流式落盘，单分片上限可配置（默认 32MB） |
| 超长文本 | 1MB 提示词 | token 成本、DB 行超限 | 提示词限 32KB（可配置），越界在提交前拒绝并提示 |
| 深链 | 插件 A→B→A 引用 | 无限递归 | op 校验拒绝循环引用；插件调用有深度计数 |
| 超宽图 | 1:100 长图 | 内存 | 上传时按最长边生成缩略图；原图只在查看时加载 |

### 2.4 空值与类型边界

| 场景 | 攻击 | 后果 | 修复 |
| --- | --- | --- | --- |
| `null` vs `undefined` | op 里 `patch: null` | 部分字段被清空（原项目 `{...node, ...patch}` 语义） | op 用**显式删除**语义（`Unset` 字段列表），不接受 `null` 隐式清空 |
| 空字符串 ID | `nodeId: ""` | 命中错误对象或被静默忽略 | 所有 ID 走 `^[A-Za-z0-9_-]{1,64}$` 校验，空即 422 |
| 数字与字符串混用 | `count: "3"` 与 `count: 3` | 比较失效（原项目大量 `String()` 转换） | schema 强类型，服务端拒绝类型不符；前端契约生成 |
| 重复 ID | 两个 add_node 同 ID | 覆盖或崩溃 | add 时已存在 → 409，或按「幂等更新」并返回 warning（显式声明策略） |
| 未知字段 | 请求带 `foo: 1` | 静默忽略（掩盖 bug） | 严格模式拒绝未知字段（OpenAPI `additionalProperties: false`） |
| 超大枚举 | `nodeType: "../../etc/passwd"` | 路径穿越 | 节点类型必须匹配 `^[a-z][a-z0-9-]*(:[a-z0-9-]+)?$` |
| 空数组 | `ops: []` | 返回歧义 | 明确语义：空即 no-op，不递增版本 |
| 泛型联合 | 节点 Spec 与 type 不匹配 | 数据不可解释 | Spec 判别联合 + 服务端按 type 解码，失败 422 |

### 2.5 并发边界

| 场景 | 攻击 | 后果 | 修复 |
| --- | --- | --- | --- |
| 同节点并发改 spec | 两端同时改模型参数 | 后写覆盖先写 | 版本号乐观并发：冲突返回 409 + 权威文档；语义冲突需用户裁决 |
| 并发删除与修改 | A 删节点，B 改节点 | 幽灵节点 | 删除是 op，修改也是 op，按 op 序应用；改已删节点 → 拒绝并返回 warning |
| 双提交同一 Run | 用户双击生成 | 双重付费 | `Idempotency-Key` + 按钮级 disabled + 服务端去重（INV-2/INV-3） |
| 并发上传同文件 | 两人同时传同图 | 重复存储 | 内容寻址：先算 hash，命中即复用（单飞模式防竞态） |
| 并发 GC 与引用 | GC 扫描时新引用建立 | 资产被误删 | GC 分两阶段：标记（记录水位）→ 延迟窗口 → 删除；窗口内的新引用会重置标记 |
| 并发限额 | 多请求同时检查配额 | 超额调用上游 | 限额用原子计数 / 分布式信号量，check 与 reserve 原子 |
| 同工作区并发 Run | 100 个 Run 同时跑 | 打爆上游被封 | 按 credential 维度并发上限 + 队列 + 公平调度 |
| 乐观更新回滚竞态 | 本地 op 在途时服务端广播他人 op | 状态错乱 | 本地 op 带 `localId`，服务端回显匹配后转正；未匹配的服务端 op 按版本序插入 |
| 插件同时启用/禁用 | 双击开关 | 双份注册 | 注册表按 pluginId 幂等；禁用走引用计数 |

### 2.6 安全边界

| 场景 | 攻击 | 后果 | 修复 |
| --- | --- | --- | --- |
| SSRF | Provider Base URL 指向 `169.254.169.254` 或内网 | 云元数据泄露 | 出网 IP 黑名单（默认禁私网/链路本地/回环），自部署可配置白名单放开 |
| SSRF（重定向） | 白名单域名 302 到内网 | 绕过校验 | 禁自动跟随重定向，或每跳重新校验目标 IP |
| 路径穿越 | 文件名 `../../etc/passwd` | 任意文件写 | Blob 路径用 hash，永不使用用户文件名（INV-4 前提） |
| 存储型 XSS | 插件/SVG/HTML 内容含 `<script>` | 主站被控 | 插件 iframe 无 `allow-same-origin`；SVG 单独域或 sanitize；`svg:vector` 节点必须过滤 `<script>/on*` |
| 图片解析漏洞 | 恶意图片触发解码器漏洞 | RCE | 只读元数据，不在原图域给 `Content-Type` 以外能力；可配置走独立域 |
| 凭据泄漏 | 日志打印 Authorization / data URL | Key 泄露 | 全局 redact 中间件 + 单测断言（INV-5） |
| 凭据泄漏（前端） | 浏览器内存 dump | Key 泄露 | 前端永不接触明文；掩码回显 |
| IDOR | 猜 assetId 下载他人资产 | 越权 | 所有读写强制 workspace 成员校验 + 对象级授权 |
| 越权写 | viewer 提交 op | 数据被改 | 中间件按角色校验；`viewer` 写操作 403（INV-8 审计） |
| 重放攻击 | 重放有副作用请求 | 重复扣费 | `Idempotency-Key` + 短时窗口；Agent 工具调用带 `callId` 去重 |
| 插件权限提升 | 插件声明小权限但调用大权限能力 | 越权 | 宿主侧按 manifest 白名单逐个方法校验，未知方法拒（INV-6） |
| 插件网络外泄 | 插件把画布内容发到外部 | 数据外泄 | 默认无网络；`network` 声明走宿主代理 + 审计；画像敏感时禁 |
| 拒绝服务 | 生成请求洪泛 | 服务不可用 | 按用户/工作区/凭据三级限流 + 队列水位保护 + 熔断 |
| 供应链 | 依赖投毒 | 全局沦陷 | `govulncheck` + `npm audit` 进 CI；镜像签名；SBOM |
| 密钥轮换 | 主密钥轮换后旧密文不可解 | 数据不可用 | 密文带 key version，支持双密钥读取 + 后台重加密 |
| 越权跨工作区 | 通过 canvasId 猜 project | 数据泄露（INV-10） | 所有查询以 workspace 为根，禁止「按 ID 直查」绕过归属校验 |
| 枚举 | 登录接口枚举用户 | 隐私 | 统一错误信息 + 速率限制 |
| 暴力破解 | 本机 Agent token | 本机被控 | token ≥18 字节随机、只监听 127.0.0.1、Origin 白名单、失败限速 |
| Token in URL | Agent token 放 query | 进日志/Referer/历史 | 强制 fragment 或 Header（原项目已修复，保持） |

### 2.7 分布式与一致性边界

| 场景 | 攻击 | 后果 | 修复 |
| --- | --- | --- | --- |
| 宕机时刻 | op 已应用，事件未广播 | 客户端永久不一致 | 事件与状态在同一事务/同一 outbox；客户端有 `Last-Event-ID` 补拉，超窗口则全量重同步 |
| 部分失败 | Run 中 3 步成功 1 步失败 | 结果不一致 | `partial` 状态显式建模；回写只针对成功步骤；UI 清楚展示 |
| 跨实例 SSE | A 实例连接，B 实例写 | 前端收不到 | eventbus 抽象为进程内 + Redis Pub/Sub（见 08-infra） |
| 幂等窗口 | 24h 后重放 | 重复副作用 | 幂等键保留窗口可配置；对成本敏感操作（生成）改用持久化唯一键（`run_attempts.request_id`）永久去重 |
| 事务边界 | 资产入库成功但节点回写失败 | 孤儿资产 | 回写失败进重试队列；孤儿资产由 GC 回收（有引用计数即不会误删） |
| 顺序 | op 乱序到达 | 文档错乱 | op 带服务端 `seq`，客户端按 seq 重排；缺失则补拉 |
| 时钟同步 | 集群节点时间漂移 | 租约误判 | 租约使用单调时钟 + 数据库时间，不用本地 wall clock |

### 2.8 生命周期边界

| 场景 | 风险 | 修复 |
| --- | --- | --- |
| 删除工作区 | 数据残留（INV-10） | 软删 + 14 天冷静期 + 硬删任务；硬删按 workspace 级联（含 Blob） |
| 删除项目 | 资产被其他画布共享 | 引用计数：删项目只删引用，Blob 由 GC 决定 |
| 删除资产 | 仍在画布中被引用 | 删除前检查 `asset_refs`，有引用则提示（或软删并标记） |
| 节点类型下线 | 已存画布无法渲染 | 保留类型注册表与渲染降级（展示占位 + 迁移提示） |
| 插件卸载 | 已存画布节点变空白 | 画布 JSON 带 `configSchema` 快照，未装插件展示「需要插件 X」 |
| 插件升级 | config schema 变更 | `configVersion` + `migrate(old,new)`，迁移失败则保留原 config 并只读 |
| 数据格式升级 | 读到未知版本 | 拒绝覆盖 + 备份（对齐原项目 AGENTS.md 纪律） |
| 账号注销 | Agent 会话/审计 | 审计保留（脱敏），其余级联删除；明确告知保留期限 |
| 迁移回滚 | 迁移后回滚旧版本 | 迁移文件必须提供 `down.sql` 且 CI 校验可回滚 |

### 2.9 用户体验边界（同样会被「打死」）

| 场景 | 风险 | 修复 |
| --- | --- | --- |
| 弱网 | op 提交失败静默丢失 | 失败重试 + 明确提示 + 保留本地草稿（离线可继续编辑，恢复后合并） |
| 断网 30s 恢复 | op 重复或丢失 | 客户端队列 + `Idempotency-Key`；断言「不丢不重」 |
| 多点触控 | 触控板手势误触 | 只在明确手势下缩放，禁用 `touch-action` 全禁 |
| 输入法 | 中文输入时触发快捷键 | 快捷键前判断 `isComposing` 与 `contenteditable`（原项目已做，保持） |
| 大图渲染 | 4K 图在弱设备卡死 | 缩略图优先 + 懒加载原图 + `decoding="async"` |
| 生成中的页面刷新 | 状态错乱 | 服务端 Run 是权威，前端只渲染；刷新后重新订阅 |
| 服务端重启 | 用户以为任务丢了 | 状态恢复后明确提示「已恢复运行中的任务」 |
| 插件崩溃 | 主页面白屏 | iframe 隔离 + `onerror` 捕获 + 自动禁用 |
| Bug 上报 | 无法定位 | 前端错误边界带 `traceId`，用户可一键复制诊断信息 |
| 超长错误信息 | UI 被撑破 | 服务端错误限长 300 字符；前端 ellipsis + 可复制详情 |
| 空状态 | 用户不知道做什么 | 每个列表都有空状态引导（对齐原项目） |
| 对齐无可用目标 | 点「左对齐」没反应，用户以为坏了 | 对齐按钮与「会不会真的动」同源：已对齐 / 选中不足 / 只读时**置灰**，而不是可点但静默无操作 |
| 连续点击对齐 | 每次位移叠加导致节点越飘越远 | 位移是**绝对目标坐标**而不是增量，重复执行是幂等的（`align.test.ts` 幂等用例） |
| 对齐后立刻撤销 | 一步撤不回来，或撤销后坐标漂移 | 对齐作为单条 `align-nodes` 命令入 undo 栈，一次 Ctrl+Z 整体还原到执行前坐标 |
| 对齐撞坐标上限 | 越界坐标被服务端 422，整批 op 被拒 | 复用统一 `move_node` op 路径：服务端 `COORD_LIMIT` 校验仍然生效，前端不另开后门 |
| 只读画布点对齐 | 观众改动了画布 | `dispatch` 在 `readOnly` 时直接返回空，UI 层同时置灰（双保险） |
| 连线分层遇到环 | 环路导致分层死循环 / 节点凭空消失 / **整个下游子图被压进同一列** | 先对强连通分量（SCC）做收缩、再在收缩后的 DAG 上按最长路径定层（迭代式 Tarjan，不递归不爆栈）：环上节点互为上下游故共享同一层，环下游的无环节点仍拿到正确层号。**只做「Kahn + 剩余节点甩到最后一层」是不够的**：环上节点入度永远 ≥1，其下游节点也永远降不到 0，会被一起塞进同一层——实测后果是「图里只要有一个环，整张图被压成一列」，分层功能当场失效（`layers.test.ts` 环路用例 5 条） |
| 分层整理把画布推走 | 整理完选区跳到别处，用户找不到自己的图 | 列坐标从**选区包围盒左上角**起算，整理前后选区锚点不变（`layers.test.ts` 锚点用例） |
| 整层节点横向撞车 | 同层节点宽窄不一，列与列互相压叠 | 列间距按「该层最宽节点 + 间距」计算，保证列之间不横向重叠（`layers.test.ts` 列宽用例） |
| 分层后再次点「分层成列」 | 已排好还继续移动、节点抖动 | 幂等：位移用绝对坐标，已成列时按钮置灰且不产生 op（`layers.test.ts` 幂等用例） |
| 同层节点原 y 相同 | 排序不稳定，每次整理顺序乱跳 | 层内先按原 y、再按原 x 兜底排序，保证顺序确定（`layeredColumnOffsets`） |
| 「分层成列」被当成「自动排版」 | 用户以为会重排全画布、清理连线交叉 | 只做「同层同列 + 列内铺开」，不删除/改道连线、不新建节点；连线交叉优化明确列为不做（见 parity 3.30 备注） |
| Shift 多选堆不出选区 | 对齐/分布/分层工具栏**永远不会出现**（按钮存在但用户到不了），用户以为功能没做 | Shift 点击未选中→追加、已选中→反选；Shift 框选→并入去重；语义由内核 `setSelection(sel,{additive})` / `toggleSelection` 单独持有，UI 只传命中集合与修饰键（`kernel.test.ts`） |

## 3. 对抗记录（红队演练，必须落到测试）

每条对抗都必须有一个**可执行**的回归用例，否则视为未修复。

| ID | 对抗动作 | 期望 | 用例位置 |
| --- | --- | --- | --- |
| ATK-01 | 提交 `x: NaN` 的 add_node | 422，`code=invalid_geometry` | `internal/graph/op_test.go` |
| ATK-02 | 同一 Idempotency-Key 连发 10 次 Run | 只创建 1 个 Run，且返回同一个 Run | `internal/exec/engine_test.go`、`internal/exec/sqlstore_test.go` |
| ATK-03 | Provider 超时但上游已扣费，客户端重试 | 重试携带同一 request_id（幂等头）；`run_attempts.request_id` 唯一 | `internal/provider/idempotency_test.go`、`internal/exec/sqlstore_test.go` |
| ATK-04 | Base URL = `http://169.254.169.254` | 拒绝，`code=ssrf_blocked` | `internal/platform/netguard_test.go` |
| ATK-05 | 上传文件名为 `../../x.png` | Blob 路径为 hash，无穿越 | `internal/asset/service_test.go` |
| ATK-06 | 日志中打入 `Authorization: Bearer sk-x` | 日志中为 `***`，原文一字不剩 | `internal/platform/redact_test.go` |
| ATK-07 | viewer 提交 op | 403（editor 可写，证明是按角色而非一律拒绝） | `internal/api/adversary_test.go` |
| ATK-08 | 用他人 workspace 的 canvasId 读取 | 404（不泄露存在性） | `internal/api/adversary_test.go` |
| ATK-09 | 插件调用未声明能力 `asset.read` | 拒绝并审计 | `internal/plugin/manifest_test.go` |
| ATK-10 | 插件尝试 `parent.document.cookie`；以及提交未声明（含原型链键）的方法名 | 抛异常（null origin）；未声明方法一律拒绝 | `web/e2e/plugin-sandbox.spec.ts`, `web/src/features/plugins/sandbox/__tests__/protocol.test.ts` |
| ATK-11 | 两客户端同时改同一节点 spec | 一方 409 并拿到权威文档；`baseVersion` 不可是「随便」语义（SQL 存储下也必须成立） | `web/e2e/conflict.spec.ts`, `internal/graph/sqlstore_rebase_test.go`, `internal/graph/replay_test.go` |
| ATK-12 | 断网 30s 内编辑 20 个节点后恢复 | 无丢无重，最终一致 | `web/e2e/offline.spec.ts` |
| ATK-13 | Agent 实时事件 + 历史快照同时到达 | `agent_items` 无重复 | `internal/agent/agent_test.go` |
| ATK-14 | 重放同 `callId` 的工具调用 | 只执行一次 | `internal/agent/agent_test.go` |
| ATK-15 | 视频任务运行中 kill 服务端再启动 | 任务继续轮询并完成 | `internal/exec/resume_test.go` |
| ATK-16 | GC 运行期间上传被引用的新资产 | 资产未被删 | `internal/asset/service_test.go` |
| ATK-17 | 日限额边界：跨时区跨日（含 DST 切换日） | 窗口按工作区时区取本地零点，不重复不跳过 | `internal/exec/quota_test.go` |
| ATK-18 | 5000 节点视口操作 | ≥55 FPS | `web/e2e/perf.spec.ts` |
| ATK-19 | 恶意 SVG 节点 | `<script>` 不执行；且不得以可执行类型下发（`internal/platform` 的单测 + e2e 双覆盖） | `web/e2e/svg-sanitize.spec.ts`, `internal/platform/contenttype_test.go` |
| ATK-20 | 1MB 提示词 | 422，且**不创建 Run、不产生上游调用** | `internal/api/adversary_test.go` |
| ATK-21 | 画布 op 日志重放与快照比对 | 完全一致（INV-1） | `internal/graph/replay_test.go` |
| ATK-22 | 删除工作区后用旧 ID 访问 | 立即 404；7 天冷静期后可恢复；到期进入清理候选 | `internal/identity/delete_test.go` |
| ATK-23 | 自定义调用脚本里写 `eval` / `Function` / `__proto__` / 拼接出的 `constructor` / 循环 / `require` | 一律拒绝，且以 4xx（不是 500）返回——脚本写错是用户输入问题 | `internal/sandbox/sandbox_test.go`, `web/e2e/sandbox-script.spec.ts` |
| ATK-24 | 脚本里设 `Authorization` 头；脚本请求 `169.254.169.254` 或任意第三方主机 | 拒绝：凭据只由平台注入，请求只能落在渠道 `baseUrl` 之下 | `internal/provider/adapter/script/script_test.go`, `web/e2e/sandbox-script.spec.ts` |
| ATK-25 | 通过节点 `meta` 写入 `__proto__` 等原型链键；或写入超过上限的键数 | 拒绝（meta 会在前端 `JSON.parse` 后变成真实对象，跨边界生效） | `internal/graph/op_test.go`, `internal/agent/translate_test.go` |
| ATK-26 | 本地直连开关：只开开关不确认风险 / 填非回环地址 / 填未知能力名 | 一律拒绝（服务端校验，绕过 UI 也无效） | `internal/workspace/direct_test.go`, `web/e2e/sandbox-script.spec.ts` |
| ATK-27 | 一键对齐：重复点击 / 已对齐再点 / 只选 1 个 / 空选 / 只读画布 / 位移里混入 `NaN` | 幂等（第二次不产生 op）；不足 2 个（分布不足 3 个）不产生 op；只读一律空；`NaN` 位移被丢弃不发给服务端 | `web/src/features/canvas/kernel/__tests__/align.test.ts` |
| ATK-28 | 对齐后按一次 Ctrl+Z | 整批节点回到对齐前坐标（对齐是单条命令，不是 N 条散装移动） | `web/src/features/canvas/kernel/__tests__/align.test.ts` |
| ATK-29 | 分层成列：连线成环 / 自环 / 多重边 / 重复点击 / 只选 1 个 / 空选 / 只读画布 / 整理后 Ctrl+Z | 环路不死循环且所有节点都有层号（结果稳定）；**环上节点同层，且环下游的无环节点仍拿到正确层号（不得被压进同一层）**；多个环汇入同一节点取最长路径；超长链不爆栈（迭代式 Tarjan）；自环与多重边不影响层号；同层同 x；重复点击幂等（第二次不产生 op）；不足 2 个不产生 op；只读一律空；整理是**单条命令**，一次 Ctrl+Z 整批回到整理前坐标 | `web/src/features/canvas/kernel/__tests__/layers.test.ts` |
| ATK-30 | 多选的入口本身被静默降级：Shift 点击节点（未选中 / 已选中）/ Shift 框选 / 不按 Shift 点击 | `additive` 必须真的生效：Shift 点击未选中 → **追加**；Shift 点击已选中 → **反选**；Shift 框选 → 并入既有选区并去重；不按 Shift → 替换。任一条退化为「静默覆盖选区」即视为功能入口失效——**多选堆不出来时，对齐/分布/分层工具栏永远不会出现**（这是「按钮存在但用户到不了」的假绿） | `web/src/features/canvas/kernel/__tests__/kernel.test.ts` |

> 红队不是一次性的：每引入一个新的外部依赖/新协议/新存储，**必须补一条对抗用例**。
> PR 模板中有「本次改动新增了哪些边界？补了哪条对抗用例？」必填项。

## 4. 死角（已知但暂不覆盖，必须有明确理由与期限）

| 死角 | 为什么暂不处理 | 风险接受方 | 复审触发条件 |
| --- | --- | --- | --- |
| 多人实时协同的冲突 UI | 本期只做单用户多端，op 语义已为 CRDT 预留 | 产品 | 出现团队场景需求 |
| 插件 WASM 沙箱 | iframe 已满足隔离要求，WASM 复杂度高 | 架构 | 出现需要高性能计算的插件 |
| 模型输出内容合规审核 | 依赖上游 Provider 审核；本地不做二次审核 | 合规 | 进入受监管市场 |
| 自建推理 | 只做外部 API 编排（范围外） | 产品 | 成本/隐私需求变化 |
| 移动端触控手势专项 | 保证可用即可 | 产品 | 移动端占比 > 20% |
| 全量 CRDT（Yjs/Automerge） | op-based 已满足当前需求，全量 CRDT 会引入不可控体积 | 架构 | 协同变成核心场景 |
| Blob 存储跨区复制 | 单区部署足够 | 运维 | 多区域用户 |

**纪律**：死角表里的每一项都必须写「复审触发条件」。没有触发条件的死角等于永久技术债。

## 5. 解耦清单（哪里必须能替换）

| 关注点 | 抽象 | 可替换实现 | 替换时的验证 |
| --- | --- | --- | --- |
| 存储后端 | `platform.BlobStore` | FS / S3 / MinIO | 契约测试跑两遍 |
| 数据库 | `store.Querier`（sqlc 接口） | SQLite / Postgres | 迁移脚本 + 集成测试双跑 |
| 队列 | `exec.Queue` | 内存 / Redis Stream | 调度器单测用假队列 |
| 实时通道 | `eventbus.Bus` | 进程内 / Redis PubSub | 双实例 e2e |
| 模型 Provider | `provider.Adapter` | openai / gemini / script | 录制样本契约测试 |
| 图像处理 | `asset.Transcoder` | 纯 Go / 外部进程 | 输出 hash 断言 |
| 时钟与 ID | `platform.Clock` / `platform.IDGen` | 真实 / 假 | 确定性测试 |
| 前端渲染 | `kernel.Renderer` | DOM+SVG / Canvas2D | 同一场景图渲染一致 |
| 插件宿主 | `plugin.Host` | iframe / Worker | 能力代理契约测试 |
| 密钥存储 | `platform.SecretStore` | DB+AES / Vault | 双实现同接口 |
| 认证 | `identity.Authenticator` | Session / OAuth / APIKey | 中间件单测 |

**验收**：每个抽象都必须有 ≥2 个实现（真实 + 测试替身），否则不算解耦，只算「预留了接口」。

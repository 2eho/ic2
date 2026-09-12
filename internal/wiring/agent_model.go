package wiring

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/agent"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// modelClient 是「服务端直接承载 Agent」形态（BackendHTTP）的模型客户端。
//
// 与 exec 的关系：Agent 复用同一套 provider 适配器与凭据解析，
// 而不是另开一条「Agent 专用」的调用路径——否则额度、重试、审计、脱敏
// 都会出现两套行为，安全边界会从这里破口。
type modelClient struct {
	providers *provider.Service
	transport *provider.Transport
	resolver  *provider.CredentialResolver
}

// newModelClient 构造模型客户端。
func newModelClient(providers *provider.Service, transport *provider.Transport, resolver *provider.CredentialResolver) *modelClient {
	return &modelClient{providers: providers, transport: transport, resolver: resolver}
}

// Complete 见 agent.ModelClient。
func (m *modelClient) Complete(ctx context.Context, req agent.CompletionRequest) (agent.CompletionResult, error) {
	_, cred, ok := m.resolver.Resolve(provider.CapTextGenerate, "", "")
	if !ok {
		return agent.CompletionResult{}, platform.NewError(422, platform.CodeInvalidRequest,
			"没有可用于 Agent 的文本模型，请先在配置中心添加具备 text.generate 能力的渠道")
	}

	// 组装上游请求：把工具定义透传给具备函数调用能力的模型。
	// 这里刻意不实现「模型不支持工具调用时自动降级为纯文本」——
	// 那会让 Agent 静默失去写画布的能力，用户只会看到「它什么都不做」。
	prompt := buildAgentPrompt(req)
	pres, err := m.callText(ctx, req.Model, prompt, cred)
	if err != nil {
		return agent.CompletionResult{}, err
	}
	return agent.CompletionResult{
		Text: pres.Text,
		Usage: agent.Usage{
			TextTokensIn:  pres.Usage.TextTokensIn,
			TextTokensOut: pres.Usage.TextTokensOut,
			CostMicros:    pres.Usage.CostMicros,
		},
	}, nil
}

// callText 走 provider 的传输层（含 SSRF 防护、退避、脱敏）。
func (m *modelClient) callText(ctx context.Context, model string, prompt string, cred provider.Credential) (provider.Response, error) {
	adapter, ok := m.resolver.Adapter(cred.ProviderID)
	if !ok {
		return provider.Response{}, platform.NewError(422, platform.CodeInvalidRequest, "渠道没有可用适配器")
	}
	req := provider.Request{
		Capability: provider.CapTextGenerate,
		Model:      model,
		Prompt:     prompt,
		RequestID:  "agent-" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", ""),
		Params:     map[string]any{"stream": false},
	}
	return adapter.Invoke(ctx, cred, req)
}

// buildAgentPrompt 把 Agent 的会话与工具表拼成上游提示词。
//
// 之所以不用 OpenAI 的原生 tools 字段：不同协议（OpenAI / Gemini）的工具调用格式差异大，
// 而在 OpenAI 兼容网关里，tools 字段的支持度参差不齐（很多中转站会丢弃它）。
// 这里采用「系统提示词里描述工具 + 要求模型输出 JSON 调用块」的保守方案，
// 保证在任意兼容渠道上都能工作；原生 tools 支持作为后续优化项。
func buildAgentPrompt(req agent.CompletionRequest) string {
	var sb strings.Builder
	sb.WriteString(req.System)
	if sb.Len() == 0 {
		sb.WriteString("你是画布助手。可以调用工具修改画布。")
	}
	sb.WriteString("\n\n可用工具：\n")
	for _, t := range req.Tools {
		sb.WriteString("- ")
		sb.WriteString(t.Name)
		if t.Scope == "write" {
			sb.WriteString("（写操作，需要用户确认）")
		}
		sb.WriteString(": ")
		sb.WriteString(t.Description)
		sb.WriteString("\n")
	}
	sb.WriteString("\n需要调用工具时，输出 ```json 代码块：{\"tool\":\"<name>\",\"arguments\":{...}}\n")
	if len(req.Snapshot) > 0 {
		sb.WriteString("\n当前画布快照：\n")
		sb.WriteString(rawJSON(req.Snapshot))
		sb.WriteString("\n")
	}
	sb.WriteString("\n对话：\n")
	for _, msg := range req.Messages {
		sb.WriteString(msg.Role)
		sb.WriteString(": ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

func rawJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

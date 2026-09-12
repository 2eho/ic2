// Package gemini 实现 Google Gemini 适配器。
// 覆盖：generateContent（文本/图片/TTS）、predictLongRunning（视频）、models（列表）。
// 对齐原项目 web/src/services/api/model-plugin.ts 的 gemini 模板行为。
package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// Adapter 是 Gemini 适配器。
type Adapter struct {
	transport *provider.Transport
}

// New 构造适配器。
func New(t *provider.Transport) *Adapter { return &Adapter{transport: t} }

// ID 见 provider.Adapter。
func (a *Adapter) ID() string { return "gemini" }

// Capabilities 见 provider.Adapter。
func (a *Adapter) Capabilities() []provider.Capability {
	return []provider.Capability{
		provider.CapImageGenerate, provider.CapImageEdit,
		provider.CapTextGenerate, provider.CapVideoGenerate,
		provider.CapAudioGenerate, provider.CapModelList,
	}
}

type genReq struct {
	Contents          []content  `json:"contents"`
	GenerationConfig  *genConfig `json:"generationConfig,omitempty"`
	SystemInstruction *content   `json:"systemInstruction,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inlineData,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type genConfig struct {
	ResponseModalities []string      `json:"responseModalities,omitempty"`
	ImageConfig        *imageConfig  `json:"imageConfig,omitempty"`
	SpeechConfig       *speechConfig `json:"speechConfig,omitempty"`
	Temperature        *float64      `json:"temperature,omitempty"`
}

type imageConfig struct {
	AspectRatio string `json:"aspectRatio,omitempty"`
	ImageSize   string `json:"imageSize,omitempty"`
}

type speechConfig struct {
	VoiceConfig struct {
		PrebuiltVoiceConfig struct {
			VoiceName string `json:"voiceName"`
		} `json:"prebuiltVoiceConfig"`
	} `json:"voiceConfig"`
}

type genResp struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text       string `json:"text"`
				InlineData *struct {
					MimeType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	Error *geminiError `json:"error"`
}

// Invoke 见 provider.Adapter。
func (a *Adapter) Invoke(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	switch req.Capability {
	case provider.CapImageGenerate, provider.CapImageEdit:
		return a.generateImages(ctx, cred, req)
	case provider.CapAudioGenerate:
		return a.tts(ctx, cred, req)
	case provider.CapTextGenerate:
		return a.text(ctx, cred, req)
	case provider.CapVideoGenerate:
		return a.videoCreate(ctx, cred, req)
	}
	return provider.Response{}, &provider.ProviderError{
		Class: provider.ClassPermanent, Code: "unsupported_capability",
		Message: "gemini adapter does not support " + string(req.Capability),
	}
}

func (a *Adapter) generateImages(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	body := genReq{
		Contents: []content{{Parts: buildParts(req)}},
		GenerationConfig: &genConfig{
			ResponseModalities: []string{"TEXT", "IMAGE"},
			ImageConfig: &imageConfig{
				AspectRatio: strParam(req.Params, "aspectRatio", ""),
				ImageSize:   strParam(req.Params, "imageSize", strParam(req.Params, "size", "")),
			},
		},
	}
	var out genResp
	if err := a.callGen(ctx, cred, req.Model, "generateContent", req.RequestID, body, &out); err != nil {
		return provider.Response{}, err
	}
	return parseGenResp(out)
}

func (a *Adapter) text(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	body := genReq{
		Contents: []content{{Parts: buildParts(req)}},
		GenerationConfig: &genConfig{
			ResponseModalities: []string{"TEXT"},
		},
	}
	var out genResp
	if err := a.callGen(ctx, cred, req.Model, "generateContent", req.RequestID, body, &out); err != nil {
		return provider.Response{}, err
	}
	return parseGenResp(out)
}

// TTS 使用 responseModalities:["AUDIO"] + speechConfig，返回 base64 PCM（原项目同语义）。
func (a *Adapter) tts(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	cfg := &genConfig{ResponseModalities: []string{"AUDIO"}}
	voice := strParam(req.Params, "audioVoice", "Kore")
	sc := &speechConfig{}
	sc.VoiceConfig.PrebuiltVoiceConfig.VoiceName = voice
	cfg.SpeechConfig = sc
	body := genReq{Contents: []content{{Parts: buildParts(req)}}, GenerationConfig: cfg}
	var out genResp
	if err := a.callGen(ctx, cred, req.Model, "generateContent", req.RequestID, body, &out); err != nil {
		return provider.Response{}, err
	}
	return parseGenResp(out)
}

type longRunningReq struct {
	Instances  []map[string]any `json:"instances"`
	Parameters map[string]any   `json:"parameters,omitempty"`
}

type longRunningResp struct {
	Name     string       `json:"name"`
	Done     bool         `json:"done"`
	Error    *geminiError `json:"error"`
	Response struct {
		GenerateVideoResponse struct {
			GeneratedSamples []struct {
				Video struct {
					URI string `json:"uri"`
				} `json:"video"`
			} `json:"generatedSamples"`
		} `json:"generateVideoResponse"`
	} `json:"response"`
	Metadata struct {
		ProgressPercent int `json:"progressPercent"`
	} `json:"metadata"`
}

func (a *Adapter) videoCreate(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	body := longRunningReq{
		Instances:  []map[string]any{{"prompt": provider.ComposePrompt(req)}},
		Parameters: buildVideoParams(req.Params),
	}
	var out longRunningResp
	if err := a.callLong(ctx, cred, req.Model, "predictLongRunning", req.RequestID, body, &out); err != nil {
		return provider.Response{}, err
	}
	if out.Error != nil {
		return provider.Response{}, &provider.ProviderError{
			Class: provider.ClassPermanent, Code: platform.CodeUpstreamInvalid, Message: out.Error.Message,
		}
	}
	return provider.Response{
		// 任务名需要持久化，服务端重启后可继续轮询（对齐原项目 videoTaskId 能力）
		RemoteTask: &provider.RemoteTask{
			ID: out.Name, Provider: "gemini", Status: "running", Progress: out.Metadata.ProgressPercent,
		},
	}, nil
}

// Poll 见 provider.Adapter：查询 long-running operation。
func (a *Adapter) Poll(ctx context.Context, cred provider.Credential, taskID string) (provider.RemoteTask, error) {
	url := fmt.Sprintf("%s/v1beta/%s", provider.TrimBaseURL(cred.BaseURL), strings.TrimPrefix(taskID, "/"))
	var out longRunningResp
	if err := a.transport.DoJSON(ctx, "GET", url, provider.RequestHeaders(cred, "gemini", ""), nil, &out); err != nil {
		return provider.RemoteTask{}, err
	}
	task := provider.RemoteTask{ID: taskID, Provider: "gemini", Status: "running", Progress: out.Metadata.ProgressPercent}
	if out.Done {
		task.Status = "succeeded"
		task.Progress = 100
	}
	if out.Error != nil {
		task.Status = "failed"
	}
	return task, nil
}

// FetchAsset 见 provider.Adapter：下载生成的视频。
func (a *Adapter) FetchAsset(ctx context.Context, cred provider.Credential, ref provider.AssetRef) (io.ReadCloser, string, error) {
	if ref.URL == "" {
		return nil, "", &provider.ProviderError{Class: provider.ClassPermanent, Code: platform.CodeInvalidRequest, Message: "asset has no url"}
	}
	return a.transport.DoRaw(ctx, "GET", ref.URL, provider.RequestHeaders(cred, "gemini", ""))
}

type modelsResp struct {
	Models []struct {
		Name                       string   `json:"name"`
		DisplayName                string   `json:"displayName"`
		SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
	} `json:"models"`
}

// ListModels 见 provider.Adapter。
func (a *Adapter) ListModels(ctx context.Context, cred provider.Credential) ([]provider.ModelInfo, error) {
	var out modelsResp
	if err := a.transport.DoJSON(ctx, "GET", provider.TrimBaseURL(cred.BaseURL)+"/v1beta/models",
		provider.RequestHeaders(cred, "gemini", ""), nil, &out); err != nil {
		return nil, err
	}
	models := make([]provider.ModelInfo, 0, len(out.Models))
	for _, m := range out.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		models = append(models, provider.ModelInfo{
			ID:           id,
			DisplayName:  m.DisplayName,
			Capabilities: GuessCapabilities(id, m.SupportedGenerationMethods),
		})
	}
	return models, nil
}

// Stream 见 provider.Adapter：streamGenerateContent?alt=sse。
func (a *Adapter) Stream(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Stream, error) {
	body := genReq{
		Contents:         []content{{Parts: buildParts(req)}},
		GenerationConfig: &genConfig{ResponseModalities: []string{"TEXT"}},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, platform.AsError(err)
	}
	headers := provider.RequestHeaders(cred, "gemini", req.RequestID)
	headers["Accept"] = "text/event-stream"
	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse",
		provider.TrimBaseURL(cred.BaseURL), req.Model)
	rc, _, err := a.transport.DoRawWithBody(ctx, "POST", url, headers, payload)
	if err != nil {
		return nil, err
	}
	return newSSEStream(rc), nil
}

// errorEnvelope 是 Gemini 错误体（不同接口的响应结构共享该字段）。
type errorEnvelope struct {
	Error *geminiError `json:"error"`
}

func (a *Adapter) callGen(ctx context.Context, cred provider.Credential, model, method, requestID string, body any, out *genResp) error {
	if err := a.postJSON(ctx, cred, model, method, requestID, body, out); err != nil {
		return err
	}
	return classifyEnvelope(out.Error)
}

func (a *Adapter) callLong(ctx context.Context, cred provider.Credential, model, method, requestID string, body any, out *longRunningResp) error {
	if err := a.postJSON(ctx, cred, model, method, requestID, body, out); err != nil {
		return err
	}
	return classifyEnvelope(out.Error)
}

func (a *Adapter) postJSON(ctx context.Context, cred provider.Credential, model, method, requestID string, body any, out any) error {
	url := fmt.Sprintf("%s/v1beta/models/%s:%s", provider.TrimBaseURL(cred.BaseURL), model, method)
	return a.transport.DoJSON(ctx, "POST", url, provider.RequestHeaders(cred, "gemini", requestID), body, out)
}

func classifyEnvelope(e *geminiError) error {
	if e == nil {
		return nil
	}
	cls := provider.ClassPermanent
	switch {
	case e.Code == 429:
		cls = provider.ClassRateLimited
	case e.Code >= 500:
		cls = provider.ClassTransient
	}
	return &provider.ProviderError{
		Class: cls, Code: platform.CodeUpstreamInvalid,
		HTTPStatus: e.Code, Message: platform.Redact(e.Message),
	}
}

func buildParts(req provider.Request) []part {
	parts := []part{}
	for _, in := range req.Inputs {
		if in.Kind == "image" && strings.HasPrefix(in.AssetID, "data:") {
			idx := strings.Index(in.AssetID, ",")
			if idx > 0 {
				mime := strings.TrimSuffix(strings.TrimPrefix(in.AssetID[:idx], "data:"), ";base64")
				parts = append(parts, part{InlineData: &inlineData{MimeType: mime, Data: in.AssetID[idx+1:]}})
			}
		}
	}
	parts = append(parts, part{Text: provider.ComposePrompt(req)})
	return parts
}

func buildVideoParams(params map[string]any) map[string]any {
	out := map[string]any{}
	if v, ok := params["aspectRatio"]; ok {
		out["aspectRatio"] = v
	}
	if v, ok := params["seconds"]; ok {
		out["durationSeconds"] = v
	}
	if v, ok := params["resolution"]; ok {
		out["resolution"] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseGenResp(out genResp) (provider.Response, error) {
	if out.PromptFeedback != nil && out.PromptFeedback.BlockReason != "" {
		return provider.Response{}, &provider.ProviderError{
			Class: provider.ClassContentPolicy, Code: platform.CodeContentPolicy,
			Message: "blocked: " + out.PromptFeedback.BlockReason,
		}
	}
	res := provider.Response{
		Usage: provider.Usage{
			TextTokensIn:  out.UsageMetadata.PromptTokenCount,
			TextTokensOut: out.UsageMetadata.CandidatesTokenCount,
		},
	}
	var text strings.Builder
	for _, c := range out.Candidates {
		for _, p := range c.Content.Parts {
			if p.Text != "" {
				text.WriteString(p.Text)
			}
			if p.InlineData != nil {
				b, err := base64.StdEncoding.DecodeString(p.InlineData.Data)
				if err != nil {
					continue
				}
				res.Assets = append(res.Assets, provider.AssetRef{
					Kind: kindOfMime(p.InlineData.MimeType), Bytes: b, MIME: p.InlineData.MimeType,
				})
			}
		}
	}
	res.Text = text.String()
	for _, a := range res.Assets {
		if a.Kind == "image" {
			res.Usage.Images++
		}
	}
	return res, nil
}

func kindOfMime(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	}
	return "file"
}

// GuessCapabilities 按模型名与 supportedGenerationMethods 推断能力。
func GuessCapabilities(model string, methods []string) []provider.Capability {
	m := strings.ToLower(model)
	caps := []provider.Capability{}
	add := func(c provider.Capability) {
		for _, x := range caps {
			if x == c {
				return
			}
		}
		caps = append(caps, c)
	}
	for _, method := range methods {
		if method == "predictLongRunning" {
			add(provider.CapVideoGenerate)
		}
	}
	switch {
	case strings.Contains(m, "imagen"), strings.Contains(m, "image"):
		add(provider.CapImageGenerate)
		add(provider.CapImageEdit)
	case strings.Contains(m, "veo"):
		add(provider.CapVideoGenerate)
	case strings.Contains(m, "tts"):
		add(provider.CapAudioGenerate)
	default:
		add(provider.CapTextGenerate)
	}
	return caps
}

func strParam(params map[string]any, key, def string) string {
	if params == nil {
		return def
	}
	v, ok := params[key]
	if !ok || v == nil {
		return def
	}
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return fmt.Sprintf("%g", s)
	case int:
		return fmt.Sprint(s)
	case bool:
		if s {
			return "true"
		}
		return "false"
	}
	return def
}

// geminiError 是 Gemini 各接口共享的错误体结构。
type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

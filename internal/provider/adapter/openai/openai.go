// Package openai 实现 OpenAI 兼容协议适配器。
// 覆盖：/v1/images/generations、/v1/images/edits、/v1/responses、/v1/videos、/v1/audio/speech、/v1/models。
// 对齐原项目 web/src/services/api/{image,video,audio}.ts 的实际协议行为。
package openai

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"strings"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// Adapter 是 OpenAI 兼容适配器。
type Adapter struct {
	transport *provider.Transport
}

// New 构造适配器。
func New(t *provider.Transport) *Adapter { return &Adapter{transport: t} }

// ID 见 provider.Adapter。
func (a *Adapter) ID() string { return "openai" }

// Capabilities 见 provider.Adapter。
func (a *Adapter) Capabilities() []provider.Capability {
	return []provider.Capability{
		provider.CapImageGenerate, provider.CapImageEdit,
		provider.CapTextGenerate, provider.CapTextTools,
		provider.CapVideoGenerate, provider.CapAudioGenerate,
		provider.CapModelList,
	}
}

// Invoke 见 provider.Adapter。
func (a *Adapter) Invoke(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	switch req.Capability {
	case provider.CapImageGenerate:
		return a.imageGenerate(ctx, cred, req)
	case provider.CapImageEdit:
		return a.imageEdit(ctx, cred, req)
	case provider.CapTextGenerate:
		return a.textGenerate(ctx, cred, req)
	case provider.CapVideoGenerate:
		return a.videoCreate(ctx, cred, req)
	case provider.CapAudioGenerate:
		return a.audioSpeech(ctx, cred, req)
	}
	return provider.Response{}, &provider.ProviderError{
		Class: provider.ClassPermanent, Code: "unsupported_capability",
		Message: "openai adapter does not support " + string(req.Capability),
	}
}

// ------------------------------------------------------------------ 图片

type imageGenReq struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Background     string `json:"background,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

type imageResp struct {
	Data []struct {
		B64JSON string `json:"b64_json"`
		URL     string `json:"url"`
		Revised string `json:"revised_prompt"`
	} `json:"data"`
	Usage struct {
		TotalTokens  int64 `json:"total_tokens"`
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

func (a *Adapter) imageGenerate(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	body := imageGenReq{
		Model:          req.Model,
		Prompt:         ComposePrompt(req),
		N:              max1(req.Count),
		Size:           strParam(req.Params, "size", "1024x1024"),
		ResponseFormat: "b64_json",
	}
	if v := strParam(req.Params, "quality", ""); v != "" {
		body.Quality = v
	}
	if v := strParam(req.Params, "background", ""); v != "" {
		body.Background = v
	}
	var out imageResp
	if err := a.transport.DoJSON(ctx, "POST", provider.TrimBaseURL(cred.BaseURL)+"/v1/images/generations",
		provider.AuthHeaders(cred), body, &out); err != nil {
		return provider.Response{}, err
	}
	return imageResponse(out, req), nil
}

func (a *Adapter) imageEdit(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	// /v1/images/edits 是 multipart。原项目曾因重复的 `image` 字段被中转站拒绝，
	// 这里统一使用 `image[]` 数组字段（见 docs/design/10 §4.3）。
	var buf strings.Builder
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", req.Model)
	_ = mw.WriteField("prompt", ComposePrompt(req))
	_ = mw.WriteField("n", fmt.Sprint(max1(req.Count)))
	_ = mw.WriteField("size", strParam(req.Params, "size", "1024x1024"))
	_ = mw.WriteField("response_format", "b64_json")
	if v := strParam(req.Params, "quality", ""); v != "" {
		_ = mw.WriteField("quality", v)
	}

	images := 0
	for _, in := range req.Inputs {
		if in.Kind != "image" || len(in.AssetID) == 0 {
			continue
		}
		// AssetID 形如 "data:<mime>;base64,<payload>" 时直接内联，否则由 exec 层预先附加
		if strings.HasPrefix(in.AssetID, "data:") {
			idx := strings.Index(in.AssetID, ",")
			if idx < 0 {
				continue
			}
			if err := writeDataURIPart(mw, "image[]", "ref.png", in.AssetID[:idx], in.AssetID[idx+1:]); err != nil {
				return provider.Response{}, platform.AsError(err)
			}
			images++
		}
	}
	if images == 0 {
		return provider.Response{}, &provider.ProviderError{
			Class: provider.ClassPermanent, Code: platform.CodeInvalidRequest,
			Message: "image.edit requires at least one reference image",
		}
	}
	if err := mw.Close(); err != nil {
		return provider.Response{}, platform.AsError(err)
	}

	headers := provider.AuthHeaders(cred)
	headers["Content-Type"] = mw.FormDataContentType()
	var out imageResp
	if err := a.transport.DoJSON(ctx, "POST", provider.TrimBaseURL(cred.BaseURL)+"/v1/images/edits",
		headers, rawMultipart(buf.String(), mw.FormDataContentType()), &out); err != nil {
		return provider.Response{}, err
	}
	return imageResponse(out, req), nil
}

func imageResponse(out imageResp, req provider.Request) provider.Response {
	res := provider.Response{Raw: map[string]any{}}
	for _, d := range out.Data {
		ref := provider.AssetRef{Kind: "image", MIME: "image/png"}
		switch {
		case d.B64JSON != "":
			b, err := base64.StdEncoding.DecodeString(d.B64JSON)
			if err != nil {
				continue
			}
			ref.Bytes = b
		case d.URL != "":
			ref.URL = d.URL
		default:
			continue
		}
		res.Assets = append(res.Assets, ref)
	}
	res.Usage.Images = int64(len(res.Assets))
	res.Usage.TextTokensIn = out.Usage.InputTokens
	res.Usage.TextTokensOut = out.Usage.OutputTokens
	if res.Usage.TextTokensIn == 0 && out.Usage.TotalTokens > 0 {
		res.Usage.TextTokensIn = out.Usage.TotalTokens
	}
	_ = req
	return res
}

// ------------------------------------------------------------------ 文本

type responsesReq struct {
	Model           string `json:"model"`
	Input           string `json:"input"`
	Stream          bool   `json:"stream"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type responsesResp struct {
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	OutputText string `json:"output_text"`
	Usage      struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

func (a *Adapter) textGenerate(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	body := responsesReq{
		Model:           req.Model,
		Input:           ComposePrompt(req),
		Stream:          false,
		ReasoningEffort: strParam(req.Params, "reasoningEffort", ""),
	}
	var out responsesResp
	if err := a.transport.DoJSON(ctx, "POST", provider.TrimBaseURL(cred.BaseURL)+"/v1/responses",
		provider.AuthHeaders(cred), body, &out); err != nil {
		return provider.Response{}, err
	}
	text := out.OutputText
	if text == "" {
		var sb strings.Builder
		for _, o := range out.Output {
			for _, c := range o.Content {
				if c.Type == "output_text" {
					sb.WriteString(c.Text)
				}
			}
		}
		text = sb.String()
	}
	return provider.Response{
		Text: text,
		Usage: provider.Usage{
			TextTokensIn:  out.Usage.InputTokens,
			TextTokensOut: out.Usage.OutputTokens,
		},
	}, nil
}

// ------------------------------------------------------------------ 视频

type videoReq struct {
	Model         string `json:"model"`
	Prompt        string `json:"prompt"`
	Seconds       string `json:"seconds,omitempty"`
	Size          string `json:"size,omitempty"`
	GenerateAudio string `json:"generate_audio,omitempty"`
	Watermark     string `json:"watermark,omitempty"`
}

type videoResp struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Error    *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (a *Adapter) videoCreate(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	body := videoReq{
		Model:         req.Model,
		Prompt:        ComposePrompt(req),
		Seconds:       strParam(req.Params, "seconds", ""),
		Size:          strParam(req.Params, "size", ""),
		GenerateAudio: strParam(req.Params, "generateAudio", ""),
		Watermark:     strParam(req.Params, "watermark", ""),
	}
	var out videoResp
	if err := a.transport.DoJSON(ctx, "POST", provider.TrimBaseURL(cred.BaseURL)+"/v1/videos",
		provider.AuthHeaders(cred), body, &out); err != nil {
		return provider.Response{}, err
	}
	if out.Error != nil {
		return provider.Response{}, &provider.ProviderError{
			Class: provider.ClassPermanent, Code: platform.CodeUpstreamInvalid, Message: out.Error.Message,
		}
	}
	return provider.Response{
		RemoteTask: &provider.RemoteTask{ID: out.ID, Provider: "openai", Status: out.Status, Progress: out.Progress},
		Usage:      provider.Usage{VideoMillis: 0},
	}, nil
}

// Poll 见 provider.Adapter。
func (a *Adapter) Poll(ctx context.Context, cred provider.Credential, taskID string) (provider.RemoteTask, error) {
	var out videoResp
	if err := a.transport.DoJSON(ctx, "GET", provider.TrimBaseURL(cred.BaseURL)+"/v1/videos/"+taskID,
		provider.AuthHeaders(cred), nil, &out); err != nil {
		return provider.RemoteTask{}, err
	}
	task := provider.RemoteTask{ID: out.ID, Provider: "openai", Status: out.Status, Progress: out.Progress}
	if out.Status == "completed" {
		task.Status = "succeeded"
	}
	return task, nil
}

// FetchAsset 见 provider.Adapter。
func (a *Adapter) FetchAsset(ctx context.Context, cred provider.Credential, ref provider.AssetRef) (io.ReadCloser, string, error) {
	if ref.URL == "" {
		return nil, "", &provider.ProviderError{Class: provider.ClassPermanent, Code: platform.CodeInvalidRequest, Message: "asset has no url"}
	}
	return a.transport.DoRaw(ctx, "GET", ref.URL, provider.AuthHeaders(cred))
}

// ------------------------------------------------------------------ 音频

type speechReq struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	Voice          string `json:"voice,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	Speed          string `json:"speed,omitempty"`
	Instructions   string `json:"instructions,omitempty"`
}

func (a *Adapter) audioSpeech(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	body := speechReq{
		Model:          req.Model,
		Input:          ComposePrompt(req),
		Voice:          strParam(req.Params, "audioVoice", "alloy"),
		ResponseFormat: strParam(req.Params, "audioFormat", "mp3"),
		Speed:          strParam(req.Params, "audioSpeed", ""),
		Instructions:   strParam(req.Params, "audioInstructions", ""),
	}
	rc, mime, err := postJSONForBinary(a.transport, ctx, provider.TrimBaseURL(cred.BaseURL)+"/v1/audio/speech",
		provider.AuthHeaders(cred), body)
	if err != nil {
		return provider.Response{}, err
	}
	defer rc.Close()
	b, err := readAllLimit(rc, 64<<20)
	if err != nil {
		return provider.Response{}, platform.AsError(err)
	}
	if mime == "" {
		mime = "audio/mpeg"
	}
	return provider.Response{
		Assets: []provider.AssetRef{{Kind: "audio", Bytes: b, MIME: mime}},
		Usage:  provider.Usage{AudioMillis: 0, Images: 0},
	}, nil
}

// ------------------------------------------------------------------ 模型

type modelsResp struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ListModels 见 provider.Adapter。
func (a *Adapter) ListModels(ctx context.Context, cred provider.Credential) ([]provider.ModelInfo, error) {
	var out modelsResp
	if err := a.transport.DoJSON(ctx, "GET", provider.TrimBaseURL(cred.BaseURL)+"/v1/models",
		provider.AuthHeaders(cred), nil, &out); err != nil {
		return nil, err
	}
	models := make([]provider.ModelInfo, 0, len(out.Data))
	for _, m := range out.Data {
		models = append(models, provider.ModelInfo{
			ID:           m.ID,
			Capabilities: GuessCapabilities(m.ID),
		})
	}
	return models, nil
}

// Stream 见 provider.Adapter（流式文本）。实现见 stream.go。
func (a *Adapter) Stream(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Stream, error) {
	return a.openStream(ctx, cred, req)
}

// GuessCapabilities 按模型名关键词猜测能力（对齐原项目 guessCapability）。
func GuessCapabilities(model string) []provider.Capability {
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
	switch {
	case strings.Contains(m, "dall-e"), strings.Contains(m, "gpt-image"), strings.Contains(m, "flux"),
		strings.Contains(m, "sd"), strings.Contains(m, "stable-diffusion"), strings.Contains(m, "imagen"):
		add(provider.CapImageGenerate)
		add(provider.CapImageEdit)
	case strings.Contains(m, "sora"), strings.Contains(m, "veo"), strings.Contains(m, "kling"),
		strings.Contains(m, "video"), strings.Contains(m, "wan"):
		add(provider.CapVideoGenerate)
	case strings.Contains(m, "tts"), strings.Contains(m, "speech"), strings.Contains(m, "audio"):
		add(provider.CapAudioGenerate)
	default:
		add(provider.CapTextGenerate)
	}
	return caps
}

// max1 保证张数至少为 1。
func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
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
		return trimFloat(s)
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

func trimFloat(f float64) string {
	s := fmt.Sprintf("%g", f)
	return s
}

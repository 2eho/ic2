package gemini

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/context-flow/ic/internal/provider"
)

func newSSEStream(rc io.ReadCloser) provider.Stream {
	return &sseStream{rc: rc, sc: bufio.NewScanner(rc)}
}

type sseStream struct {
	rc     io.ReadCloser
	sc     *bufio.Scanner
	closed bool
}

// Recv 见 provider.Stream。
func (s *sseStream) Recv() (string, bool, error) {
	if s.closed {
		return "", true, nil
	}
	s.sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for s.sc.Scan() {
		line := strings.TrimSpace(s.sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		var out genResp
		if err := json.Unmarshal([]byte(data), &out); err != nil {
			continue
		}
		if out.Error != nil {
			return "", true, &provider.ProviderError{
				Class: provider.ClassPermanent, Code: "upstream_invalid", Message: out.Error.Message,
			}
		}
		var sb strings.Builder
		for _, c := range out.Candidates {
			for _, p := range c.Content.Parts {
				sb.WriteString(p.Text)
			}
		}
		if sb.Len() > 0 {
			return sb.String(), false, nil
		}
	}
	if err := s.sc.Err(); err != nil {
		return "", true, provider.ClassifyTransport(err)
	}
	return "", true, nil
}

// Close 见 provider.Stream。
func (s *sseStream) Close() error {
	s.closed = true
	return s.rc.Close()
}

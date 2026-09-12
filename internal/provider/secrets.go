package provider

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/context-flow/ic/internal/platform"
)

// SecretStore 是凭据存储抽象。解耦清单要求 ≥2 个实现：AES+DB / 测试替身。
type SecretStore interface {
	// Seal 加密明文，返回可落库的密文串（带 key version，支持轮换）。
	Seal(plaintext string) (string, error)
	// Open 解密。
	Open(sealed string) (string, error)
}

// envelope 是密文封装：v 为密钥版本，n 为 nonce，c 为密文。
// 带版本号是为了支持双密钥期读取旧密文（见 11 §2.6 密钥轮换）。
type envelope struct {
	V int    `json:"v"`
	N string `json:"n"`
	C string `json:"c"`
}

// AESSecretStore 是 AES-256-GCM 实现。
type AESSecretStore struct {
	mu      sync.RWMutex
	primary int
	keys    map[int][]byte
}

// NewAESSecretStore 用主密钥构造（支持传入多个历史密钥用于轮换期读取）。
func NewAESSecretStore(primary []byte, previous ...[]byte) (*AESSecretStore, error) {
	if len(primary) != 32 {
		return nil, fmt.Errorf("主密钥必须是 32 字节，当前 %d", len(primary))
	}
	s := &AESSecretStore{primary: 1, keys: map[int][]byte{1: primary}}
	ver := 0
	for _, p := range previous {
		if len(p) != 32 {
			continue
		}
		ver--
		s.keys[ver] = p
	}
	return s, nil
}

// Seal 见 SecretStore。
func (s *AESSecretStore) Seal(plaintext string) (string, error) {
	s.mu.RLock()
	ver := s.primary
	key := s.keys[ver]
	s.mu.RUnlock()

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", platform.AsError(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", platform.AsError(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", platform.AsError(err)
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	env := envelope{V: ver, N: base64.StdEncoding.EncodeToString(nonce), C: base64.StdEncoding.EncodeToString(ct)}
	b, err := json.Marshal(env)
	if err != nil {
		return "", platform.AsError(err)
	}
	return "ic1:" + base64.StdEncoding.EncodeToString(b), nil
}

// Open 见 SecretStore。
func (s *AESSecretStore) Open(sealed string) (string, error) {
	if !strings.HasPrefix(sealed, "ic1:") {
		return "", platform.NewError(500, platform.CodeInternal, "unsupported secret format")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, "ic1:"))
	if err != nil {
		return "", platform.NewError(500, platform.CodeInternal, "secret is corrupted")
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", platform.NewError(500, platform.CodeInternal, "secret is corrupted")
	}
	s.mu.RLock()
	key, ok := s.keys[env.V]
	s.mu.RUnlock()
	if !ok {
		return "", platform.NewError(500, platform.CodeInternal, "secret key version is unavailable").
			WithDetail("keyVersion", env.V)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", platform.AsError(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", platform.AsError(err)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.N)
	if err != nil {
		return "", platform.NewError(500, platform.CodeInternal, "secret nonce is corrupted")
	}
	ct, err := base64.StdEncoding.DecodeString(env.C)
	if err != nil {
		return "", platform.NewError(500, platform.CodeInternal, "secret payload is corrupted")
	}
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", platform.NewError(500, platform.CodeInternal, "secret authentication failed").WithCause(err)
	}
	return string(pt), nil
}

// Rotate 轮换主密钥：旧密钥保留用于读取，新写入使用新密钥（双密钥期）。
func (s *AESSecretStore) Rotate(newKey []byte) error {
	if len(newKey) != 32 {
		return fmt.Errorf("新主密钥必须是 32 字节")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.primary + 10
	s.keys[next] = newKey
	s.primary = next
	return nil
}

// PrimaryVersion 返回当前写入使用的密钥版本（可观测项）。
func (s *AESSecretStore) PrimaryVersion() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.primary
}

// PlainSecretStore 是开发/测试用实现（不做加密，且会显式标记）。
// 生产环境禁止使用：由 cmd 装配时校验。
type PlainSecretStore struct{}

// Seal 见 SecretStore。
func (PlainSecretStore) Seal(plaintext string) (string, error) { return "plain:" + plaintext, nil }

// Open 见 SecretStore。
func (PlainSecretStore) Open(sealed string) (string, error) {
	return strings.TrimPrefix(sealed, "plain:"), nil
}

package platform

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"

	"github.com/google/uuid"
)

// IDGen 生成实体 ID。测试可用确定性实现（见 11 §5 解耦清单）。
type IDGen interface {
	NewID(prefix string) string
}

type uuidGen struct{}

func (uuidGen) NewID(prefix string) string {
	if prefix == "" {
		return uuid.NewString()
	}
	return prefix + "_" + uuid.NewString()
}

// DefaultIDGen 返回默认实现。
func DefaultIDGen() IDGen { return uuidGen{} }

// SeqIDGen 是确定性替身，便于断言。
type SeqIDGen struct {
	Prefix string
	N      int
}

// NewID 返回 prefix_序号。
func (g *SeqIDGen) NewID(prefix string) string {
	g.N++
	p := prefix
	if p == "" {
		p = g.Prefix
	}
	return p + "_" + strconv.Itoa(g.N)
}

// RandomToken 生成 n 字节的十六进制随机串，用于本机 Agent token 等（>=18 字节，见 11 §2.6）。
func RandomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

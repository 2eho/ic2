package provider

import (
	"strings"
	"testing"
)

func key(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	s, err := NewAESSecretStore(key(1))
	if err != nil {
		t.Fatal(err)
	}
	for _, pt := range []string{"sk-abcdef123456", "短", "héllo wörld 🎨", ""} {
		sealed, err := s.Seal(pt)
		if err != nil {
			t.Fatalf("seal(%q): %v", pt, err)
		}
		if strings.Contains(sealed, pt) && pt != "" {
			t.Fatalf("密文包含明文: %s", sealed)
		}
		got, err := s.Open(sealed)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if got != pt {
			t.Fatalf("roundtrip 失败: %q != %q", got, pt)
		}
	}
}

// 同一明文两次加密应产生不同密文（nonce 随机）。
func TestSealIsNonDeterministic(t *testing.T) {
	s, _ := NewAESSecretStore(key(1))
	a, _ := s.Seal("same")
	b, _ := s.Seal("same")
	if a == b {
		t.Fatal("密文不应相同")
	}
}

// 篡改密文必须解密失败（GCM 认证）。
func TestTamperedCiphertextFails(t *testing.T) {
	s, _ := NewAESSecretStore(key(1))
	sealed, _ := s.Seal("secret")
	if _, err := s.Open(sealed[:len(sealed)-4] + "AAAA"); err == nil {
		t.Fatal("篡改后应解密失败")
	}
	if _, err := s.Open("garbage"); err == nil {
		t.Fatal("非法格式应失败")
	}
}

// 密钥轮换：旧密文仍可读，新写入使用新版本。
func TestKeyRotationDualRead(t *testing.T) {
	s, _ := NewAESSecretStore(key(1))
	old, _ := s.Seal("old-secret")
	v1 := s.PrimaryVersion()

	if err := s.Rotate(key(2)); err != nil {
		t.Fatal(err)
	}
	if s.PrimaryVersion() == v1 {
		t.Fatal("版本应已变更")
	}
	// 旧密文可读
	got, err := s.Open(old)
	if err != nil {
		t.Fatalf("轮换后旧密文应可读: %v", err)
	}
	if got != "old-secret" {
		t.Fatalf("got=%q", got)
	}
	// 新密文用新密钥
	fresh, _ := s.Seal("new-secret")
	if fresh == old {
		t.Fatal("新密文应不同")
	}
	if got, _ := s.Open(fresh); got != "new-secret" {
		t.Fatalf("新密文读取失败: %q", got)
	}
}

func TestKeyLengthValidation(t *testing.T) {
	if _, err := NewAESSecretStore([]byte("short")); err == nil {
		t.Fatal("短密钥应被拒绝")
	}
	s, _ := NewAESSecretStore(key(1))
	if err := s.Rotate([]byte("short")); err == nil {
		t.Fatal("轮换到短密钥应被拒绝")
	}
}

func TestUnavailableKeyVersion(t *testing.T) {
	s, _ := NewAESSecretStore(key(1))
	sealed, _ := s.Seal("x")
	// 用另一实例（只有 key 2）读取，应提示密钥版本不可用而不是静默失败
	other, _ := NewAESSecretStore(key(2))
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("缺少对应密钥版本应报错")
	}
}

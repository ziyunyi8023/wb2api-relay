package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 32)
	if err := SetEncryptionKey(key); err != nil {
		t.Fatal(err)
	}
	defer SetEncryptionKey(nil)

	plain := []byte(`{"auth":{"accessToken":"tok","refreshToken":"ref"},"account":{"uid":"u1"}}`)
	sealed := SealStorage(plain)
	if bytes.Equal(sealed, plain) {
		t.Fatal("sealed == plain, 加密未生效")
	}
	if !bytes.HasPrefix(sealed, encMagic) {
		t.Fatalf("sealed missing magic: %q", sealed[:16])
	}
	out, err := OpenStorage(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatalf("round trip mismatch: %q", out)
	}
}

func TestOpenStoragePlainPassthrough(t *testing.T) {
	defer SetEncryptionKey(nil)
	SetEncryptionKey(nil)
	plain := []byte(`{"accessToken":"x"}`)
	out, err := OpenStorage(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatal("plain must pass through untouched")
	}
	// 有密钥时明文仍透传（存量兼容）
	if err := SetEncryptionKey(bytes.Repeat([]byte{0x22}, 32)); err != nil {
		t.Fatal(err)
	}
	out2, err := OpenStorage(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out2, plain) {
		t.Fatal("plain must pass through with key set")
	}
}

func TestOpenStorageWrongKey(t *testing.T) {
	if err := SetEncryptionKey(bytes.Repeat([]byte{0x33}, 32)); err != nil {
		t.Fatal(err)
	}
	sealed := SealStorage([]byte("payload"))
	if err := SetEncryptionKey(bytes.Repeat([]byte{0x44}, 32)); err != nil {
		t.Fatal(err)
	}
	defer SetEncryptionKey(nil)
	if _, err := OpenStorage(sealed); err == nil {
		t.Fatal("wrong key must fail")
	}
}

func TestOpenStorageCipherNoKey(t *testing.T) {
	if err := SetEncryptionKey(bytes.Repeat([]byte{0x55}, 32)); err != nil {
		t.Fatal(err)
	}
	sealed := SealStorage([]byte("payload"))
	SetEncryptionKey(nil)
	if _, err := OpenStorage(sealed); err == nil {
		t.Fatal("ciphertext without key must fail loudly")
	}
}

func TestLoadOrCreateKeyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "crypto.secret")
	k1, err := LoadOrCreateKeyFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != 32 {
		t.Fatalf("key len %d", len(k1))
	}
	k2, err := LoadOrCreateKeyFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatal("key file must be stable across loads")
	}
	// 坏文件报错而非静默换钥
	if err := os.WriteFile(p, []byte("zz"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKeyFile(p); err == nil {
		t.Fatal("corrupt key file must error")
	}
}

// TestSaveAtomicSealedRoundTrip 端到端：密钥就绪 → SaveAtomic 写密文 → Parse 还原。
func TestSaveAtomicSealedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := SetEncryptionKey(bytes.Repeat([]byte{0x66}, 32)); err != nil {
		t.Fatal(err)
	}
	defer SetEncryptionKey(nil)

	a := &Auth{
		AccessToken:  "at",
		RefreshToken: "rt",
		ExpiresAt:    123,
		Domain:       "www.workbuddy.ai",
		UID:          "uid-1",
		Nickname:     "nick",
		FilePath:     filepath.Join(dir, "workbuddy-uid-1.json"),
	}
	if err := a.SaveAtomic(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(a.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, encMagic) {
		t.Fatalf("on-disk form must be sealed, got: %.40q", raw)
	}
	b, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if b.AccessToken != "at" || b.RefreshToken != "rt" || b.UID != "uid-1" {
		t.Fatalf("parsed mismatch: %+v", b)
	}
}

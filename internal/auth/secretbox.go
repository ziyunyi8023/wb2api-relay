package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// secretbox — auths 磁盘加密（AES-256-GCM）。
//
// 磁盘形态：明文 JSON 与密文并存。密文 = "WBAPIENC1" 魔数 + 12B nonce + GCM 密文。
// 读路径（Parse/OpenStorage）遇到魔数解密，否则原样当明文（存量与 login.sh 手写文件兼容）；
// 写路径（SaveAtomic/SealStorage）在密钥就绪时一律写密文。密钥来源：SetEncryptionKey
// （cmd/server 启动时从 crypto.secret 装载，缺失则自动生成 32B 十六进制）。
// 无密钥时 SealStorage 退化为透传——测试与裸用场景零门槛。

var (
	encMu     sync.RWMutex
	encKey    []byte // 16/24/32B；nil = 加密关闭（透传）
	encGCM    cipher.AEAD
	encMagic  = []byte("WBAPIENC1")
	nonceSize = 12
)

// SetEncryptionKey 设置 AES 密钥（16/24/32 字节）；传 nil 关闭加密。
func SetEncryptionKey(key []byte) error {
	encMu.Lock()
	defer encMu.Unlock()
	if len(key) == 0 {
		encKey, encGCM = nil, nil
		return nil
	}
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return fmt.Errorf("crypto key length %d want 16/24/32", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	encKey, encGCM = key, g
	return nil
}

// EncryptionEnabled 报告当前是否启用落盘加密。
func EncryptionEnabled() bool {
	encMu.RLock()
	defer encMu.RUnlock()
	return encKey != nil
}

// SealStorage 明文 → 密文（无密钥时透传）。
func SealStorage(plain []byte) []byte {
	encMu.RLock()
	g := encGCM
	encMu.RUnlock()
	if g == nil {
		return plain
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return plain // 熵源异常时宁可写明文也不丢账号
	}
	out := make([]byte, 0, len(encMagic)+nonceSize+len(plain)+g.Overhead())
	out = append(out, encMagic...)
	out = append(out, nonce...)
	out = g.Seal(out[:len(encMagic)+nonceSize], nonce, plain, nil)
	return out
}

// OpenStorage 密文 → 明文；非密文（无魔数）原样返回。魔数在场但密钥缺失/错误时报错。
func OpenStorage(raw []byte) ([]byte, error) {
	if len(raw) < len(encMagic) || !strings.EqualFold(string(raw[:len(encMagic)]), string(encMagic)) {
		return raw, nil
	}
	encMu.RLock()
	g := encGCM
	encMu.RUnlock()
	if g == nil {
		return nil, errors.New("auths storage is encrypted but no crypto key loaded (check crypto.secret)")
	}
	body := raw[len(encMagic):]
	if len(body) < nonceSize+g.Overhead() {
		return nil, errors.New("auths storage ciphertext truncated")
	}
	nonce, ct := body[:nonceSize], body[nonceSize:]
	plain, err := g.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("auths storage decrypt failed (crypto.secret mismatch?): %w", err)
	}
	return plain, nil
}

// LoadOrCreateKeyFile 从 hex 文件装载 32B 密钥；文件缺失时生成并写回（0600）。
// 已有文件长度/解析异常视为致命（避免静默换钥后全部旧密文解不开）。
func LoadOrCreateKeyFile(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil {
		s := strings.TrimSpace(string(b))
		key, err := hex.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("crypto.secret hex 解析失败: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("crypto.secret 长度 %d 字节，want 32", len(key))
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("crypto.secret 写入失败: %w", err)
	}
	return key, nil
}

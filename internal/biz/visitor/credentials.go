package visitor

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
)

const visitorTokenPrefix = "v1."

// credentialProtector 仅保护客户端持有的 VisitorToken，不再保存平台身份密文。
type credentialProtector struct{ visitorAEAD cipher.AEAD }

// newCredentialProtector 从部署根密钥创建访问凭据保护器，缺少根密钥时拒绝启动。
func newCredentialProtector(secret string) (*credentialProtector, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, errors.New("identity token encryption secret is required")
	}
	visitorAEAD, err := deriveAEAD(secret, "visitor-credential")
	if err != nil {
		return nil, err
	}
	return &credentialProtector{visitorAEAD: visitorAEAD}, nil
}

// deriveAEAD 用用途标识与根密钥派生 AES-GCM 密钥，与管理员 JWT 使用不同用途。
func deriveAEAD(secret, purpose string) (cipher.AEAD, error) {
	key := sha256.Sum256([]byte(purpose + "\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal 生成随机 nonce 并与认证密文一起编码，同一明文每次加密结果不同。
func seal(aead cipher.AEAD, plaintext []byte) (string, error) {
	if aead == nil {
		return "", errors.New("identity protector is not configured")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(append(nonce, ciphertext...)), nil
}

// open 解码并验证 AES-GCM 完整性，格式或认证失败统一返回 ErrUnauthorized。
func open(aead cipher.AEAD, value string) ([]byte, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || aead == nil || len(payload) < aead.NonceSize() {
		return nil, ErrUnauthorized
	}
	plaintext, err := aead.Open(nil, payload[:aead.NonceSize()], payload[aead.NonceSize():], nil)
	if err != nil {
		return nil, ErrUnauthorized
	}
	return plaintext, nil
}

// sealVisitor 加密完整访问身份载荷并添加协议版本前缀。
func (p *credentialProtector) sealVisitor(payload []byte) (string, error) {
	encoded, err := seal(p.visitorAEAD, payload)
	if err != nil {
		return "", err
	}
	return visitorTokenPrefix + encoded, nil
}

// openVisitor 只接受当前协议前缀的访问凭据，再使用访问专用密钥解密。
func (p *credentialProtector) openVisitor(token string) ([]byte, error) {
	if !strings.HasPrefix(token, visitorTokenPrefix) {
		return nil, ErrUnauthorized
	}
	return open(p.visitorAEAD, strings.TrimPrefix(token, visitorTokenPrefix))
}

// TikTokOpenIDHash 生成应用范围内的平台用户标识摘要。
func TikTokOpenIDHash(applicationID, openID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(applicationID) + "\x00" + strings.TrimSpace(openID)))
	return hex.EncodeToString(sum[:])
}

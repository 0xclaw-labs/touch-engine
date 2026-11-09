// Package wecom implements the WeCom (企业微信) callback message crypto:
// the signature scheme and the AES-256-CBC message envelope used by the
// 「客户事件」回调 (HUI-4589 验签骨架).
//
// 协议（官方加解密方案）：
//   - signature = sha1(lexicographic(token, timestamp, nonce, encrypt) joined)
//   - EncodingAESKey: 43 个 base64 字符 + '=' 解出 32 字节 AES key
//   - 明文包: 16 字节随机 + 4 字节网络序消息长度 + 消息 + receiveid，PKCS7 填充
//
// 纪律：这是纯协议实现（可被本地 fixture 对拍）；凭据缺失时调用方（回调
// 端点）必须诚实「待资质」，绝不伪造验证结果。
package wecom

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrBadSignature marks a signature verification failure.
var ErrBadSignature = errors.New("wecom: msg_signature mismatch")

// ErrBadKey marks a malformed EncodingAESKey.
var ErrBadKey = errors.New("wecom: EncodingAESKey must be 43 base64 chars")

// Signature computes the callback signature over the four sorted parts.
func Signature(token, timestamp, nonce, encrypt string) string {
	parts := []string{token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	h := sha1.Sum([]byte(strings.Join(parts, "")))
	return fmt.Sprintf("%x", h)
}

// VerifySignature reports whether the provided signature matches.
func VerifySignature(token, timestamp, nonce, encrypt, msgSignature string) bool {
	return Signature(token, timestamp, nonce, encrypt) == msgSignature
}

// aesKey derives the 32-byte key from the 43-char EncodingAESKey.
func aesKey(encodingAESKey string) ([]byte, error) {
	raw := strings.TrimSpace(encodingAESKey)
	if len(raw) != 43 {
		return nil, ErrBadKey
	}
	key, err := base64.StdEncoding.DecodeString(raw + "=")
	if err != nil || len(key) != 32 {
		return nil, ErrBadKey
	}
	return key, nil
}

// pkcs7Unpad removes the PKCS7 padding (block size 32 per protocol).
func pkcs7Unpad(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	pad := int(b[len(b)-1])
	if pad <= 0 || pad > 32 || pad > len(b) {
		return b
	}
	return b[:len(b)-pad]
}

// pkcs7Pad adds PKCS7 padding to blockSize.
func pkcs7Pad(b []byte, blockSize int) []byte {
	pad := blockSize - len(b)%blockSize
	avoid := make([]byte, pad)
	for i := range avoid {
		avoid[i] = byte(pad)
	}
	return append(b, avoid...)
}

// Decrypt opens one callback envelope. It returns the plaintext message and
// the receiveid (corp id) so the caller can check which app the event is for.
func Decrypt(encodingAESKey, encryptB64 string) (msg, receiveID string, err error) {
	key, err := aesKey(encodingAESKey)
	if err != nil {
		return "", "", err
	}
	cipherText, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encryptB64))
	if err != nil || len(cipherText) < aes.BlockSize || len(cipherText)%aes.BlockSize != 0 {
		return "", "", fmt.Errorf("wecom: encrypt payload malformed")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	iv := key[:aes.BlockSize]
	plain := make([]byte, len(cipherText))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, cipherText)
	plain = pkcs7Unpad(plain)
	if len(plain) < 20 {
		return "", "", fmt.Errorf("wecom: decrypted envelope too short")
	}
	msgLen := binary.BigEndian.Uint32(plain[16:20])
	if int(msgLen)+20 > len(plain) {
		return "", "", fmt.Errorf("wecom: decrypted length mismatch")
	}
	msg = string(plain[20 : 20+msgLen])
	receiveID = string(plain[20+msgLen:])
	return msg, receiveID, nil
}

// Encrypt builds one envelope (protocol symmetry; exercised by local fixtures
// and useful for wiring self-tests — production never calls it).
func Encrypt(encodingAESKey, msg, receiveID string) (string, error) {
	key, err := aesKey(encodingAESKey)
	if err != nil {
		return "", err
	}
	random := []byte("0123456789abcdef") // 16 bytes; fixtures only
	buf := make([]byte, 0, len(random)+4+len(msg)+len(receiveID))
	buf = append(buf, random...)
	var lenBytes [4]byte
	binary.BigEndian.PutUint32(lenBytes[:], uint32(len(msg)))
	buf = append(buf, lenBytes[:]...)
	buf = append(buf, []byte(msg)...)
	buf = append(buf, []byte(receiveID)...)
	buf = pkcs7Pad(buf, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	iv := key[:aes.BlockSize]
	out := make([]byte, len(buf))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, buf)
	return base64.StdEncoding.EncodeToString(out), nil
}

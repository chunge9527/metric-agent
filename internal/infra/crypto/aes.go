// Package crypto AES加密实现
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"metric-agent/internal/infra/iface"
)

// AESCrypto AES加密实现（CBC模式 + PKCS7填充）
type AESCrypto struct {
	// cipher.Block 在创建时初始化并缓存（密钥不变，block 可复用，aes 实现并发安全）
	block cipher.Block
}

// NewAESCrypto 创建AESCrypto实例
// key长度支持16/24/32字节（对应AES-128/192/256）
func NewAESCrypto(key string) (*AESCrypto, error) {
	keyBytes := []byte(key)
	keyLen := len(keyBytes)

	switch keyLen {
	case 16, 24, 32:
		// 合法的AES密钥长度
	default:
		return nil, fmt.Errorf("无效的AES密钥长度: %d，支持16/24/32字节", keyLen)
	}

	// 密钥校验通过后创建一次并缓存，避免每次加解密重建
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("创建AES cipher失败: %w", err)
	}

	return &AESCrypto{block: block}, nil
}

// Encrypt 加密明文
// 采用AES-CBC模式，PKCS7填充，加密结果前附加IV
func (a *AESCrypto) Encrypt(plaintext []byte) ([]byte, error) {
	// PKCS7填充
	padded := pkcs7Pad(plaintext, aes.BlockSize)

	// 生成随机IV
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("生成IV失败: %w", err)
	}

	// CBC加密
	ciphertext := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(a.block, iv)
	mode.CryptBlocks(ciphertext, padded)

	// 拼接IV + 密文
	result := append(iv, ciphertext...)
	return result, nil
}

// Decrypt 解密密文
// 从密文前提取IV，使用AES-CBC解密并去除PKCS7填充
func (a *AESCrypto) Decrypt(ciphertext []byte) ([]byte, error) {
	// 检查密文长度
	if len(ciphertext) < aes.BlockSize {
		return nil, fmt.Errorf("密文长度不足")
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("密文长度不是block size的整数倍")
	}

	// 提取IV和实际密文
	iv := ciphertext[:aes.BlockSize]
	encrypted := ciphertext[aes.BlockSize:]

	// CBC解密
	padded := make([]byte, len(encrypted))
	mode := cipher.NewCBCDecrypter(a.block, iv)
	mode.CryptBlocks(padded, encrypted)

	// 去除PKCS7填充
	plaintext, err := pkcs7Unpad(padded, aes.BlockSize)
	if err != nil {
		return nil, fmt.Errorf("去除PKCS7填充失败: %w", err)
	}

	return plaintext, nil
}

// pkcs7Pad PKCS7填充
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padText := make([]byte, padding)
	for i := range padText {
		padText[i] = byte(padding)
	}
	return append(data, padText...)
}

// pkcs7Unpad 去除PKCS7填充
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("数据为空")
	}
	if len(data)%blockSize != 0 {
		return nil, fmt.Errorf("数据长度不是block size的整数倍")
	}

	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize {
		return nil, fmt.Errorf("无效的填充值: %d", padding)
	}
	// 验证所有填充字节
	for i := len(data) - padding; i < len(data); i++ {
		if data[i] != byte(padding) {
			return nil, fmt.Errorf("无效的填充内容")
		}
	}

	return data[:len(data)-padding], nil
}

// 确保实现Crypto接口
var _ iface.Crypto = (*AESCrypto)(nil)

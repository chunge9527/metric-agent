package iface

// Crypto 加解密接口
// 定义对称加密能力，用于远程命令的安全传输
type Crypto interface {
	// Encrypt 加密明文
	// plaintext: 待加密的明文字节
	// 返回: 加密后的密文字节，或错误
	Encrypt(plaintext []byte) ([]byte, error)

	// Decrypt 解密密文
	// ciphertext: 待解密的密文字节
	// 返回: 解密后的明文字节，或错误
	Decrypt(ciphertext []byte) ([]byte, error)
}

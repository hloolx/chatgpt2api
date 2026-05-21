package cloudstorage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// 严格对应 Kotlin 的 GCMParameterSpec(96, iv)
const tagSize = 12
const ivSize = 12

// GenerateRandomByteArray 对应 SecureRandom().nextBytes()
func GenerateRandomByteArray(size int) ([]byte, error) {
	byteArray := make([]byte, size)
	_, err := io.ReadFull(rand.Reader, byteArray)
	if err != nil {
		return nil, err
	}
	return byteArray, nil
}

// getGCM 对应 Kotlin 的 getCipher 逻辑
// Go 将 Cipher 的 init 和模式封装在了 AEAD 接口中
func getGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	// 必须使用 NewGCMWithTagSize 强制指定为 12 字节 (96 bit)
	return cipher.NewGCMWithTagSize(block, tagSize)
}

// EncryptAES 对应 Kotlin 的 encryptAES
func EncryptAES(data []byte, key []byte) ([]byte, error) {
	aesGcm, err := getGCM(key)
	if err != nil {
		return nil, err
	}

	iv, err := GenerateRandomByteArray(ivSize)
	if err != nil {
		return nil, err
	}

	// Seal(dst, nonce, plaintext, additionalData)
	// 第一个参数传 iv，结果会是 [iv][ciphertext][tag]
	return aesGcm.Seal(iv, iv, data, nil), nil
}

// DecryptAES 对应 Kotlin 的 decryptAES(ByteArray)
func DecryptAES(data []byte, key []byte) ([]byte, error) {
	if len(data) <= ivSize {
		return nil, errors.New("data too short")
	}

	aesGcm, err := getGCM(key)
	if err != nil {
		return nil, err
	}

	iv := data[:ivSize]
	ciphertextWithTag := data[ivSize:]

	// Open 会自动根据 tagSize 校验末尾的字节
	return aesGcm.Open(nil, iv, ciphertextWithTag, nil)
}

// DecryptAESStream 对应 Kotlin 的 suspend fun decryptAES (ByteReadChannel)
func DecryptAESStream(reader io.Reader, key []byte, limit int) ([]byte, error) {
	// 1. 读取 12 字节 IV
	iv := make([]byte, ivSize)
	if _, err := io.ReadFull(reader, iv); err != nil {
		return nil, fmt.Errorf("read iv failed: %v", err)
	}

	// 2. 准备读取加密数据 + Tag
	// Kotlin 中 size >= limit + 12 的判断
	// 这里预分配缓冲区，注意 GCM 必须拿全数据才能解密
	aesGcm, err := getGCM(key)
	if err != nil {
		return nil, err
	}

	// 限制读取总量: 用户数据限制 + 12字节Tag
	maxToRead := int64(limit + tagSize)

	// 使用带限制的 Reader 模拟 Kotlin 的 size 检查
	limitedReader := io.LimitReader(reader, maxToRead+1) // 多读一个字节用来判断是否超标

	encryptedData, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, err
	}

	// 检查是否超过了 limit + tagSize
	if len(encryptedData) > int(maxToRead) {
		return nil, errors.New("分片文件过大") // 对应 NoRetryException
	}

	// 3. 执行解密并校验
	return aesGcm.Open(nil, iv, encryptedData, nil)
}

package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/curve25519"
)

func GenerateKeyPair() (priv, pub []byte, err error) {
	priv = make([]byte, 32)
	if _, err = io.ReadFull(rand.Reader, priv); err != nil {
		return nil, nil, err
	}
	pub, err = curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, nil, err
	}
	return priv, pub, nil
}

func DH(priv, pub []byte) ([]byte, error) {
	return curve25519.X25519(priv, pub)
}

func HMACSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func SHA512Sum(data []byte) []byte {
	h := sha512.Sum512(data)
	return h[:]
}

func KDF3(secret []byte) (cipherKey, nextChainKey, macKey []byte) {
	var empty [32]byte
	chainKey := HMACSHA256(empty[:], secret)
	out := HMACSHA256(chainKey, empty[:])
	cipherKey = HMACSHA256(chainKey, out)[:32]
	nextChainKey = HMACSHA256(chainKey, append(append([]byte{}, out...), 0x01))
	macKey = out[:32]
	return cipherKey, nextChainKey, macKey
}

func KDF160(secret []byte) (macKey, chainKey []byte) {
	var empty [32]byte
	chainKey = HMACSHA256(empty[:], secret)
	out := HMACSHA256(chainKey, empty[:])
	feedback := append(append([]byte{}, out...), 0x07)
	part2 := HMACSHA256(chainKey, feedback)
	full := append(append([]byte{}, out...), part2...)
	return full[:16], chainKey
}

func EncryptAES256CTR(key, iv, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(plain))
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(out, plain)
	return out, nil
}

func DecryptAES256CTR(key, iv, data []byte) ([]byte, error) {
	return EncryptAES256CTR(key, iv, data)
}

func EncryptGCM(key, iv, plain, adata []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, iv, plain, adata), nil
}

func DecryptGCM(key, iv, data, adata []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, iv, data, adata)
}

func RandomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	return b
}

var ErrShortData = errors.New("data too short")

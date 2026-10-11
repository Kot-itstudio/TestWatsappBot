package wa

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"strconv"

	"github.com/user/wabot/internal/crypto"
)

func randomID() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<47))
	if err != nil {
		return strconv.FormatInt(int64(len(crypto.RandomBytes(6))), 16)
	}
	return fmt.Sprintf("%014X", n)
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func parseIntDefault(s string) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func generateNoiseKey() (priv, pub []byte, err error) {
	return crypto.GenerateKeyPair()
}

package wa

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/user/wabot/internal/crypto"
)

const (
	waPrologue = "WA\x20" +
		"\x06" +
		"\xf7\xf2\xc3\xa9\xd4\xb1\xf5\x4f\x24\x73\xc1\x5b\x21\xe1\x81\x0e" +
		"\xa3\x86\x07\x1d\xbe\x28\xc9\xea\xef\xca\xfa\x2f\x3c\xec\x8c\x9c" +
		"\x2e\x1c\x4b\xdb\x5e\x3f\x11\xcc\x7b\x2e\xc5\x1e\xef\x6f\x40\x2f" +
		"\x27\x35\xcf\x8d\x8f\xfe\x1e\xde\x1e\x0b\x9c\x2a\x42\xd9\xad\x44" +
		"\xb7\x6d\x4d\x2a\x2e\x5c\x3c\x4e\xfb\x7b\x3d\x7c\xba\x0e\xee\xbb" +
		"\x1e\xdd\x1e\xed\x1e\xed\x1e\xed\x1e\xed\x1e\xed"
)

type Keys struct {
	IdentityPub  []byte `json:"identity_pub"`
	IdentityPriv []byte `json:"identity_priv"`
	StaticPub    []byte `json:"static_pub"`
	StaticPriv   []byte `json:"static_priv"`
	WGKey        []byte `json:"wg_key"`
	CurBlob      []byte `json:"cur_blob"`
	RegistrationID uint32 `json:"registration_id"`
}

func GenerateKeys() (*Keys, error) {
	idPriv, idPub, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	stPriv, stPub, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	wg := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, wg); err != nil {
		return nil, err
	}
	var regIDBuf [3]byte
	if _, err := io.ReadFull(rand.Reader, regIDBuf[:]); err != nil {
		return nil, err
	}
	regID := binary.BigEndian.Uint32(regIDBuf[:]) & 0x3FFFFF
	return &Keys{
		IdentityPub:    idPub,
		IdentityPriv:   idPriv,
		StaticPub:      stPub,
		StaticPriv:     stPriv,
		WGKey:          wg,
		RegistrationID: regID,
	}, nil
}

type Transport struct {
	conn      net.Conn
	send      *cryptoChain
	recv      *cryptoChain
	mu        sync.Mutex
	closed    bool
}

type cryptoChain struct {
	key   []byte
	chain []byte
	count uint32
}

func newCryptoChain(key, chain []byte) *cryptoChain {
	return &cryptoChain{key: key, chain: chain}
}

func (c *cryptoChain) encrypt(plain []byte) ([]byte, error) {
	iv := make([]byte, 12)
	binary.LittleEndian.PutUint32(iv[8:], c.count)
	ct, err := crypto.EncryptGCM(c.key, iv, plain, nil)
	if err != nil {
		return nil, err
	}
	c.advance()
	return ct, nil
}

func (c *cryptoChain) decrypt(data []byte) ([]byte, error) {
	iv := make([]byte, 12)
	binary.LittleEndian.PutUint32(iv[8:], c.count)
	pt, err := crypto.DecryptGCM(c.key, iv, data, nil)
	if err != nil {
		return nil, err
	}
	c.advance()
	return pt, nil
}

func (c *cryptoChain) advance() {
	var zero [32]byte
	next := crypto.HMACSHA256(c.chain, zero[:])
	c.key = crypto.HMACSHA256(c.chain, next)[:32]
	c.chain = next
	c.count++
}

func DialWithKeys(addr string, keys *Keys) (*Transport, error) {
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("не удалось подключиться к %s: %w", addr, err)
	}
	t := &Transport{conn: conn}
	if err := t.handshake(keys); err != nil {
		conn.Close()
		return nil, err
	}
	return t, nil
}

func (t *Transport) handshake(keys *Keys) error {
	if _, err := t.conn.Write([]byte(waPrologue)); err != nil {
		return fmt.Errorf("не удалось отправить пролог: %w", err)
	}
	act1 := buildAct1(keys.StaticPub)
	if _, err := t.conn.Write(act1); err != nil {
		return fmt.Errorf("не удалось отправить act1: %w", err)
	}
	act2 := make([]byte, 32+16)
	if _, err := io.ReadFull(t.conn, act2); err != nil {
		return fmt.Errorf("не удалось прочитать act2: %w", err)
	}
	rsPub, dhShared, err := parseAct2(keys.StaticPriv, act2)
	if err != nil {
		return err
	}
	act3, sendKey, sendChain, recvKey, recvChain, err := buildAct3(dhShared, rsPub, keys)
	if err != nil {
		return err
	}
	if _, err := t.conn.Write(act3); err != nil {
		return fmt.Errorf("не удалось отправить act3: %w", err)
	}
	serverHello := make([]byte, 3)
	if _, err := io.ReadFull(t.conn, serverHello); err != nil {
		return fmt.Errorf("не удалось прочитать приветствие сервера: %w", err)
	}
	t.send = newCryptoChain(sendKey, sendChain)
	t.recv = newCryptoChain(recvKey, recvChain)
	return nil
}

func buildAct1(staticPub []byte) []byte {
	ephPriv, ephPub, err := crypto.GenerateKeyPair()
	if err != nil {
		panic(fmt.Sprintf("noise keypair: %v", err))
	}
	_ = ephPriv
	h := crypto.SHA512Sum([]byte(handshakeName))
	remoteBind := append(h[:32], staticPub...)
	rbi := crypto.SHA512Sum(remoteBind)
	localBind := append(rbi[:32], ephPub...)
	lbi := crypto.SHA512Sum(localBind)
	cipherKey, chainKey, _ := crypto.KDF3(lbi[32:])
	payload := cipherKey[:0]
	_ = payload
	enc, err := crypto.EncryptGCM(chainKey[:32], make([]byte, 12), lbi[32:64], h[:32])
	if err != nil {
		panic(fmt.Sprintf("act1 encrypt: %v", err))
	}
	out := []byte{0xf6, 0x00, 0x03}
	out = append(out, enc...)
	return out
}

func parseAct2(staticPriv, act2 []byte) (rsPub, dhShared []byte, err error) {
	if len(act2) < 48 {
		return nil, nil, errors.New("act2 слишком короткий")
	}
	ephPriv, ephPub, kerr := crypto.GenerateKeyPair()
	if kerr != nil {
		return nil, nil, kerr
	}
	_ = ephPriv
	_ = ephPub
	rsPub = act2[:32]
	dhShared, err = crypto.DH(staticPriv, rsPub)
	if err != nil {
		return nil, nil, fmt.Errorf("dh act2: %w", err)
	}
	return rsPub, dhShared, nil
}

func buildAct3(dhShared, rsPub []byte, keys *Keys) (packet []byte, sendKey, sendChain, recvKey, recvChain []byte, err error) {
	sig, serr := crypto.DH(keys.StaticPriv, rsPub)
	if serr != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("dh act3: %w", serr)
	}
	finalDh, derr := crypto.DH(keys.StaticPriv, rsPub)
	if derr != nil {
		return nil, nil, nil, nil, nil, derr
	}
	combined := append(append([]byte{}, dhShared...), sig...)
	combined = append(combined, finalDh...)
	h := crypto.SHA512Sum([]byte(handshakeName))
	kdfInput := crypto.SHA512Sum(append(h[32:], combined...))
	tempKeys, chainA, _ := crypto.KDF3(kdfInput[32:])
	sendKey = tempKeys
	sendChain = chainA
	tempRecv, chainB, _ := crypto.KDF3(chainA)
	recvKey = tempRecv
	recvChain = chainB
	iv := make([]byte, 12)
	plainSig := keys.StaticPub
	encSig, eerr := crypto.EncryptGCM(sendKey, iv, plainSig, h[:32])
	if eerr != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("act3 encrypt: %w", eerr)
	}
	packet = []byte{0xf6, 0x00, byte(len(encSig)+16)}
	packet = append(packet, encSig...)
	packet = append(packet, make([]byte, 16)...)
	return packet, sendKey, sendChain, recvKey, recvChain, nil
}

const handshakeName = "Noise_XK_secp256k1_ChaChaPoly_SHA256"

func (t *Transport) SendFrame(frame []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("транспорт закрыт")
	}
	encrypted, err := t.send.encrypt(frame)
	if err != nil {
		return fmt.Errorf("шифрование фрейма: %w", err)
	}
	header := make([]byte, 3)
	header[0] = 0
	header[1] = byte(len(encrypted) >> 8)
	header[2] = byte(len(encrypted))
	if _, err := t.conn.Write(append(header, encrypted...)); err != nil {
		return fmt.Errorf("запись фрейма: %w", err)
	}
	return nil
}

func (t *Transport) Receive() ([]byte, error) {
	for {
		header := make([]byte, 3)
		if _, err := io.ReadFull(t.conn, header); err != nil {
			return nil, err
		}
		size := int(header[1])<<8 | int(header[2])
		data := make([]byte, size)
		if _, err := io.ReadFull(t.conn, data); err != nil {
			return nil, err
		}
		decrypted, err := t.recv.decrypt(data)
		if err != nil {
			return nil, fmt.Errorf("расшифрование фрейма: %w", err)
		}
		if len(decrypted) == 0 {
			continue
		}
		if decrypted[0]&0xf0 == 0xf0 {
			log.Printf("[wa] получен служебный stanza-кадр, игнорирую")
			continue
		}
		return decrypted, nil
	}
}

func (t *Transport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	return t.conn.Close()
}

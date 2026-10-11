package noise

import (
	"encoding/binary"
	"errors"

	"github.com/user/wabot/internal/crypto"
)

const (
	Version      = 6
	HandshakeTag = 0xf8

	MsgMax = 16400

	handshakeID = "Noise_XX_25519_ChaChaPoly_BLAKE2b"
)

var ErrBadMAC = errors.New("bad mac")

func frameNeed(bytesLeft int) int {
	switch {
	case bytesLeft <= 255:
		return 3
	case bytesLeft <= 65535:
		return 4
	default:
		return 5
	}
}

func WriteFrameHeader(buf []byte, version uint8, payloadLen int) int {
	n := frameNeed(payloadLen)
	buf[0] = HandshakeTag<<4&0xff | version&0x0f
	switch n {
	case 3:
		buf[1] = byte(payloadLen)
		buf[2] = 0
	case 4:
		buf[1] = 0
		binary.BigEndian.PutUint16(buf[2:4], uint16(payloadLen))
	case 5:
		buf[1] = 0
		binary.BigEndian.PutUint16(buf[2:4], 0)
		binary.BigEndian.PutUint32(buf[3:7], uint32(payloadLen))
	}
	return n
}

func ReadFrameHeader(data []byte) (version uint8, isHandshake bool, size int, consumed int, err error) {
	if len(data) < 3 {
		return 0, false, 0, 0, crypto.ErrShortData
	}
	v := data[0] >> 4 & 0x0f
	hs := data[0]>>4 == HandshakeTag
	n := frameNeed(len(data))
	switch n {
	case 3:
		size = int(data[1])
		consumed = 3
	case 4:
		if len(data) < 4 {
			return 0, false, 0, 0, crypto.ErrShortData
		}
		size = int(binary.BigEndian.Uint16(data[2:4]))
		consumed = 4
	case 5:
		if len(data) < 7 {
			return 0, false, 0, 0, crypto.ErrShortData
		}
		size = int(binary.BigEndian.Uint32(data[3:7]))
		consumed = 5
	}
	return v, hs, size, consumed, nil
}

type CipherState struct {
	key     []byte
	chain   []byte
	counter uint32
	hasKey  bool
}

func NewCipherState() *CipherState {
	return &CipherState{}
}

func (c *CipherState) SetKeys(key, chain []byte) {
	c.key = key
	c.chain = chain
	c.counter = 0
	c.hasKey = true
}

func (c *CipherState) Encrypt(ad, plain []byte) ([]byte, error) {
	if !c.hasKey {
		return append([]byte{}, plain...), nil
	}
	iv := make([]byte, 12)
	binary.LittleEndian.PutUint32(iv[8:], c.counter)
	ct, err := crypto.EncryptGCM(c.key, iv, plain, ad)
	if err != nil {
		return nil, err
	}
	c.advanceChain()
	return ct, nil
}

func (c *CipherState) Decrypt(ad, ct []byte) ([]byte, error) {
	if !c.hasKey {
		return append([]byte{}, ct...), nil
	}
	iv := make([]byte, 12)
	binary.LittleEndian.PutUint32(iv[8:], c.counter)
	pt, err := crypto.DecryptGCM(c.key, iv, ct, ad)
	if err != nil {
		return nil, err
	}
	c.advanceChain()
	return pt, nil
}

func (c *CipherState) advanceChain() {
	var zero [32]byte
	next := crypto.HMACSHA256(c.chain, zero[:])
	c.key = crypto.HMACSHA256(c.chain, next)[:32]
	c.chain = next
	c.counter++
}

type SymmetricState struct {
	k    []byte
	n    []byte
	hh   []byte
	cs   *CipherState
	init bool
}

func newSymmetricState(protocolName string) *SymmetricState {
	s := &SymmetricState{cs: NewCipherState()}
	h := sha256Sum([]byte(protocolName))
	if len(h) > 32 {
		s.hh = h[:32]
	} else {
		pad := make([]byte, 32-len(h))
		s.hh = append(pad, h...)
	}
	s.k = make([]byte, 32)
	s.n = make([]byte, 12)
	s.init = true
	return s
}

func sha256Sum(data []byte) []byte {
	sum := crypto.HMACSHA256(nil, data)
	return sum
}

func (s *SymmetricState) mixHash(data []byte) {
	s.hh = sha256Sum(append(append([]byte{}, s.hh...), data...))
}

func (s *SymmetricState) encryptAndHash(plain []byte) ([]byte, error) {
	if !s.cs.hasKey {
		s.mixHash(plain)
		return append([]byte{}, plain...), nil
	}
	ct, err := s.cs.Encrypt(s.hh, plain)
	if err != nil {
		return nil, err
	}
	s.mixHash(ct)
	return ct, nil
}

func (s *SymmetricState) decryptAndHash(ct []byte) ([]byte, error) {
	if !s.cs.hasKey {
		s.mixHash(ct)
		return append([]byte{}, ct...), nil
	}
	pt, err := s.cs.Decrypt(s.hh, ct)
	if err != nil {
		return nil, err
	}
	s.mixHash(ct)
	return pt, nil
}

func (s *SymmetricState) split() (*CipherState, *CipherState) {
	tempKey := crypto.HMACSHA256(s.k, s.hh)
	tk1 := crypto.HMACSHA256(tempKey, []byte{0x01})
	k1 := tk1[:32]
	tk2 := crypto.HMACSHA256(tempKey, append(append([]byte{}, tk1...), 0x02))
	k2 := tk2[:32]
	c1 := NewCipherState()
	c1.SetKeys(k1, append([]byte{}, tk1...))
	c2 := NewCipherState()
	c2.SetKeys(k2, append([]byte{}, tk2...))
	return c1, c2
}

type KeyPair struct {
	Pub     []byte
	PrivKey []byte
}

func (k *KeyPair) Priv() []byte { return k.PrivKey }

type XX struct {
	ss         *SymmetricState
	rs         *KeyPair
	re         *KeyPair
	send, recv *CipherState
	handshook  bool
	connected  bool
	buffer     []byte
}

func NewXX(localStatic *KeyPair) *XX {
	return &XX{
		ss: newSymmetricState(handshakeID),
		rs: localStatic,
	}
}

func (n *XX) Start() {
	n.ss.mixHash(n.rs.Pub)
}

func (n *XX) WriteAct1() ([]byte, error) {
	ephPriv, ephPub, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	n.re = &KeyPair{Pub: ephPub, PrivKey: ephPriv}
	ct, err := n.ss.encryptAndHash(ephPub)
	if err != nil {
		return nil, err
	}
	return ct, nil
}

func (n *XX) ReadAct2(data []byte) error {
	if len(data) < 32 {
		return errors.New("act2 too short")
	}
	ephPub, err := n.ss.decryptAndHash(data[:32])
	if err != nil {
		return err
	}
	n.re.Pub = ephPub
	dh1, err := crypto.DH(n.rs.Priv(), n.re.Pub)
	if err != nil {
		return err
	}
	n.ss.mixHash(dh1)
	return nil
}

func (n *XX) WriteAct3() ([]byte, error) {
	dh2, err := crypto.DH(n.re.Priv(), n.rs.Pub)
	if err != nil {
		return nil, err
	}
	n.ss.mixHash(dh2)
	sig, err := n.ss.encryptAndHash(n.rs.Pub)
	if err != nil {
		return nil, err
	}
	dh3, err := crypto.DH(n.re.Priv(), n.rs.Pub)
	if err != nil {
		return nil, err
	}
	n.ss.mixHash(dh3)
	mac, err := n.ss.encryptAndHash(nil)
	if err != nil {
		return nil, err
	}
	n.send, n.recv = n.ss.split()
	n.handshook = true
	return append(append(sig, mac...), 0), nil
}


func (n *XX) Send(in []byte) ([]byte, error) {
	return n.send.Encrypt(nil, in)
}

func (n *XX) Recv(in []byte) ([]byte, error) {
	return n.recv.Decrypt(nil, in)
}

func (n *XX) IsHandshook() bool {
	return n.handshook
}

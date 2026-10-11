package proto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	dictListEnd = 0x00
	dictPacked  = 236
	dictBinary  = 244
	dictDict    = 245
	numpack     = 248
	byte8       = 252
	nibble8     = 253
	binary8     = 254
	binary16    = 255
)

var ErrBadToken = errors.New("bad binary token")

type Decoder struct {
	data   []byte
	pos    int
	dict   *Dictionary
	closed bool
}

func NewDecoder(data []byte, dict *Dictionary) *Decoder {
	return &Decoder{data: data, dict: dict}
}

func (d *Decoder) Token() (*Token, error) {
	if d.closed {
		return nil, errors.New("decoder closed")
	}
	if d.pos >= len(d.data) {
		return nil, ErrEOF
	}
	token, err := d.readToken()
	if err != nil {
		return nil, err
	}
	if token.Type == TypeStreamEnd {
		d.closed = true
	}
	return token, nil
}

func (d *Decoder) readToken() (*Token, error) {
	value, err := d.ReadByte()
	if err != nil {
		return nil, err
	}
	switch {
	case value <= 223:
		return &Token{Type: TypeNil}, nil
	case value == dictListEnd:
		return &Token{Type: TypeListEnd}, nil
	case value >= 240 && value <= 243:
		return &Token{Type: TypeSubStart}, nil
	case value == dictPacked:
		return d.readPacked()
	case value == numpack:
		return d.readNumPack()
	case value == byte8:
		return d.readByte8()
	case value == nibble8:
		return d.readNibble8()
	case value == binary8:
		return d.readBinary(1)
	case value == binary16:
		return d.readBinary(2)
	case value == dictBinary:
		count, err := d.readNBytes(1)
		if err != nil {
			return nil, err
		}
		index, err := d.dict.GetIndex(int(count))
		if err != nil {
			return nil, err
		}
		return &Token{Type: TypeBinary, Value: index}, nil
	case value == dictDict:
		count, err := d.readNBytes(1)
		if err != nil {
			return nil, err
		}
		index, err := d.dict.GetIndex(int(count))
		if err != nil {
			return nil, err
		}
		return &Token{Type: TypeDictionary, Value: index}, nil
	default:
		return d.readAttribute(int(value))
	}
}

func (d *Decoder) readAttribute(token int) (*Token, error) {
	switch {
	case token >= 224 && token <= 235:
		size := token - 224
		list := List{Size: size + 1, Attributes: make([]string, size+1)}
		return &Token{Type: TypeList, Value: list}, nil
	case token >= 128 && token <= 191:
		key, err := d.dict.GetKey(token - 128)
		if err != nil {
			return nil, err
		}
		return &Token{Type: TypeKey, Value: key}, nil
	case token >= 192 && token <= 223:
		key, err := d.dict.GetKeySingle(token - 192)
		if err != nil {
			return nil, err
		}
		return &Token{Type: TypeKey, Value: key}, nil
	case token == 247:
		return d.readListLength(true)
	case token == 246:
		return d.readListLength(false)
	default:
		return nil, fmt.Errorf("%w: %d", ErrBadToken, token)
	}
}

func (d *Decoder) readListLength(isDictionary bool) (*Token, error) {
	n, err := d.readNBytes(1)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return &Token{Type: TypeDictionary, Value: ""}, nil
	}
	tok := TypeList
	if isDictionary {
		tok = TypeDictionary
	}
	return &Token{Type: tok, Value: List{Size: int(n)}}, nil
}

func (d *Decoder) readPacked() (*Token, error) {
	header, err := d.ReadByte()
	if err != nil {
		return nil, err
	}
	high := header >> 4
	low := header & 0x0f
	if high == 0 && low == 0 {
		return nil, errors.New("invalid packed header")
	}
	var values []interface{}
	for i := 0; i < int(high); i++ {
		v, err := d.ReadByte()
		if err != nil {
			return nil, err
		}
		values = append(values, int(v))
	}
	for i := 0; i < int(low); i++ {
		v, err := d.ReadByte()
		if err != nil {
			return nil, err
		}
		values = append(values, decodeNibbles(v))
	}
	return &Token{Type: TypePacked, Value: values}, nil
}

func decodeNibbles(b byte) string {
	first := b >> 4
	second := b & 0x0f
	return digitFor(first) + digitFor(second)
}

func digitFor(n byte) string {
	if n == 15 {
		return ""
	}
	return string('0' + n)
}

func (d *Decoder) readNumPack() (*Token, error) {
	header, err := d.ReadByte()
	if err != nil {
		return nil, err
	}
	isNegative := header&0x80 != 0
	if isNegative {
		header &= 0x7f
	}
	packed := make([]byte, (header+1)/2)
	for i := range packed {
		v, err := d.ReadByte()
		if err != nil {
			return nil, err
		}
		packed[i] = v
	}
	digits := make([]byte, 0, len(packed)*2)
	for _, b := range packed {
		digits = append(digits, '0'+b>>4, '0'+b&0x0f)
	}
	if header%2 == 1 {
		digits = digits[:len(digits)-1]
	}
	num := string(digits)
	if num == "" {
		num = "0"
	}
	if isNegative {
		num = "-" + num
	}
	return &Token{Type: TypeStringRaw, Value: num}, nil
}

func (d *Decoder) readByte8() (*Token, error) {
	v, err := d.ReadByte()
	if err != nil {
		return nil, err
	}
	return &Token{Type: TypeByteRaw, Value: int(v)}, nil
}

func (d *Decoder) readNibble8() (*Token, error) {
	v, err := d.ReadByte()
	if err != nil {
		return nil, err
	}
	return &Token{Type: TypeNibbleRaw, Value: int(v)}, nil
}

func (d *Decoder) readBinary(nBytes int) (*Token, error) {
	length, err := d.readNBytes(nBytes)
	if err != nil {
		return nil, err
	}
	data := make([]byte, length)
	if _, err := d.Read(data); err != nil {
		return nil, err
	}
	return &Token{Type: TypeBinary, Value: string(data)}, nil
}

func (d *Decoder) ReadByte() (byte, error) {
	if d.pos >= len(d.data) {
		return 0, ErrEOF
	}
	b := d.data[d.pos]
	d.pos++
	return b, nil
}

func (d *Decoder) Read(p []byte) (int, error) {
	n := copy(p, d.data[d.pos:])
	d.pos += n
	if n == 0 && len(p) > 0 {
		return 0, ErrEOF
	}
	return n, nil
}

func (d *Decoder) readNBytes(n int) (uint64, error) {
	buf := make([]byte, n)
	if _, err := d.Read(buf); err != nil {
		return 0, err
	}
	var val uint64
	for _, b := range buf {
		val = val<<8 | uint64(b)
	}
	return val, nil
}

var ErrEOF = errors.New("unexpected end of input")

type Encoder struct {
	buf  bytes.Buffer
	dict *Dictionary
}

func NewEncoder(dict *Dictionary) *Encoder {
	return &Encoder{dict: dict}
}

func (e *Encoder) Bytes() []byte {
	return e.buf.Bytes()
}

func (e *Encoder) writeByte(b byte) {
	e.buf.WriteByte(b)
}

func (e *Encoder) writeNode(node *Node) error {
	if node.Tag == "" && len(node.Attrs) == 0 && len(node.Content) == 0 {
		return errors.New("empty node")
	}
	e.writeByte(dictListEnd)
	e.writeByte(dictListEnd)
	e.writeByte(dictListEnd)
	e.writeByte(dictListEnd)
	e.writeByte(dictListEnd)
	if err := e.writeTag(node.Tag); err != nil {
		return err
	}
	if err := e.writeAttributes(node.Attrs); err != nil {
		return err
	}
	if len(node.Content) == 0 {
		e.writeByte(dictListEnd)
		return nil
	}
	if err := e.writeContent(node.Content); err != nil {
		return err
	}
	e.writeByte(dictListEnd)
	return nil
}

func (e *Encoder) writeTag(tag string) error {
	if tag == "" {
		e.writeByte(dictListEnd)
		return nil
	}
	if idx, ok := e.dict.Tokens[tag]; ok {
		e.writeByte(byte(idx))
		return nil
	}
	return e.writeString(tag)
}

func (e *Encoder) writeAttributes(attrs []Attribute) error {
	if len(attrs) == 0 {
		e.writeByte(dictListEnd)
		return nil
	}
	n := len(attrs)
	if n > 255 {
		return errors.New("too many attributes")
	}
	if n < 20 {
		e.writeByte(byte(224 + n))
	} else {
		e.writeByte(247)
		e.writeByte(byte(n))
	}
	for _, a := range attrs {
		if err := e.writeKeyValue(a.Key, a.Value); err != nil {
			return err
		}
	}
	return nil
}

func (e *Encoder) writeKeyValue(key, value string) error {
	if err := e.writeKey(key); err != nil {
		return err
	}
	if value == "" {
		e.writeByte(dictListEnd)
		return nil
	}
	return e.writeString(value)
}

func (e *Encoder) writeKey(key string) error {
	if idx, ok := e.dict.Keys[key]; ok {
		e.writeKeyIndex(idx)
		return nil
	}
	return e.writeString(key)
}

func (e *Encoder) writeKeyIndex(idx int) {
	if idx < 240 {
		e.writeByte(byte(128 + idx))
	} else {
		e.writeByte(236)
		e.writeByte(byte(idx-240))
	}
}

func (e *Encoder) writeString(s string) error {
	if idx, ok := e.dict.Strings[s]; ok {
		e.writeByte(dictBinary)
		e.writeByte(byte(idx))
		return nil
	}
	n := len(s)
	switch {
	case n < 256:
		e.writeByte(binary8)
		e.writeByte(byte(n))
	case n < 65536:
		e.writeByte(binary16)
		var tmp [2]byte
		binary.BigEndian.PutUint16(tmp[:], uint16(n))
		e.buf.Write(tmp[:])
	default:
		return errors.New("string too long")
	}
	e.buf.WriteString(s)
	return nil
}

func (e *Encoder) writeContent(content []*Node) error {
	for _, c := range content {
		if len(c.Attrs) == 0 && len(c.Content) == 0 {
			if err := e.writeString(c.Tag); err != nil {
				return err
			}
			continue
		}
		if err := e.writeNode(c); err != nil {
			return err
		}
	}
	return nil
}

func Encode(node *Node, dict *Dictionary) ([]byte, error) {
	enc := NewEncoder(dict)
	if err := enc.writeNode(node); err != nil {
		return nil, err
	}
	return enc.Bytes(), nil
}

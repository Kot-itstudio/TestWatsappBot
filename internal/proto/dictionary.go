package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
)

type Dictionary struct {
	Keys      map[string]int
	Tokens    map[string]int
	Strings   map[string]int
	TagList   []string
	KeyList   []string
	DictList  []string
	SingleNt  []string
}

func NewDictionary() *Dictionary {
	d := &Dictionary{
		Keys:    map[string]int{},
		Tokens:  map[string]int{},
		Strings: map[string]int{},
	}
	for i, t := range tagList {
		d.Tokens[t] = i
		d.TagList = append(d.TagList, t)
	}
	for i, k := range keyList {
		d.Keys[k] = i
		d.KeyList = append(d.KeyList, k)
	}
	for i, s := range dictList {
		d.Strings[s] = i
		d.DictList = append(d.DictList, s)
	}
	d.SingleNt = singleNByteTokens
	return d
}

func (d *Dictionary) GetKey(index int) (string, error) {
	if index < 0 || index >= len(d.KeyList) {
		return "", fmt.Errorf("bad key index %d", index)
	}
	return d.KeyList[index], nil
}

func (d *Dictionary) GetKeySingle(index int) (string, error) {
	if index < 0 || index >= len(d.SingleNt) {
		return "", fmt.Errorf("bad single byte token index %d", index)
	}
	return d.SingleNt[index], nil
}

func (d *Dictionary) GetIndex(index int) (string, error) {
	if index < 0 || index >= len(d.DictList) {
		return "", fmt.Errorf("bad dictionary index %d", index)
	}
	return d.DictList[index], nil
}

func (d *Dictionary) DecodeJID(raw string) (user, agent, device string, err error) {
	if raw == "" {
		return "", "", "", errors.New("empty jid")
	}
	parts := splitJID(raw)
	return parts[0], parts[1], parts[2], nil
}

func splitJID(raw string) [3]string {
	var out [3]string
	at := -1
	for i := 0; i < len(raw); i++ {
		if raw[i] == '@' {
			at = i
			break
		}
	}
	if at < 0 {
		out[0] = raw
		return out
	}
	out[0] = raw[:at]
	rest := raw[at+1:]
	dot := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		out[1] = rest
		return out
	}
	out[1] = rest[:dot]
	out[2] = rest[dot+1:]
	return out
}

func BinEncode(n int, length int) []byte {
	buf := make([]byte, length)
	switch length {
	case 1:
		buf[0] = byte(n)
	case 2:
		binary.BigEndian.PutUint16(buf, uint16(n))
	case 3:
		buf[0] = byte(n >> 16)
		binary.BigEndian.PutUint16(buf[1:], uint16(n))
	case 4:
		binary.BigEndian.PutUint32(buf, uint32(n))
	}
	return buf
}

package proto

import (
	"errors"
	"fmt"
)

type TokenType int

const (
	TypeNil TokenType = iota
	TypeDictionary
	TypeStreamEnd
	TypeList
	TypeListEnd
	TypeAttribute
	TypeKey
	TypeBinary
	TypePacked
	TypeSubStart
	TypeByteRaw
	TypeNibbleRaw
	TypeStringRaw
)

type List struct {
	Size       int
	Attributes []string
}

type Token struct {
	Type  TokenType
	Value interface{}
}

func (t *Token) GetString() string {
	if s, ok := t.Value.(string); ok {
		return s
	}
	return fmt.Sprint(t.Value)
}

func ParseTree(decoder *Decoder) (*Node, error) {
	root, err := parseNode(decoder)
	if err != nil {
		return nil, err
	}
	return root, nil
}

func parseNode(decoder *Decoder) (*Node, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token.Type {
	case TypeList:
		list := token.Value.(List)
		if list.Size == 0 {
			return &Node{}, nil
		}
		return parseList(list, decoder)
	case TypeDictionary:
		dict := token.Value.(List)
		if dict.Size == 0 {
			return &Node{}, nil
		}
		return parseDictionary(dict, decoder)
	default:
		return nil, fmt.Errorf("expected node, got %v", token.Type)
	}
}

func parseDictionary(dict List, decoder *Decoder) (*Node, error) {
	tag, err := parseString(decoder)
	if err != nil {
		return nil, err
	}
	node := &Node{Tag: tag}
	attrs, err := parseAttributes(decoder)
	if err != nil {
		return nil, err
	}
	node.Attrs = attrs
	content, err := parseContent(decoder)
	if err != nil {
		return nil, err
	}
	node.Content = content
	return node, nil
}

func parseList(list List, decoder *Decoder) (*Node, error) {
	if list.Size == 3 {
		tag, err := parseString(decoder)
		if err != nil {
			return nil, err
		}
		node := &Node{Tag: tag}
		attrs, err := parseAttributes(decoder)
		if err != nil {
			return nil, err
		}
		node.Attrs = attrs
		content, err := parseContent(decoder)
		if err != nil {
			return nil, err
		}
		node.Content = content
		return node, nil
	}
	var children []*Node
	for i := 0; i < list.Size; i++ {
		child, err := parseContent(decoder)
		if err != nil {
			return nil, err
		}
		children = append(children, child...)
	}
	if len(children) == 1 && children[0] != nil {
		return children[0], nil
	}
	return &Node{Tag: "", Content: children}, nil
}

func parseAttributes(decoder *Decoder) ([]Attribute, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token.Type == TypeListEnd {
		return nil, nil
	}
	var list List
	switch token.Type {
	case TypeList:
		list = token.Value.(List)
	case TypeDictionary:
		list = token.Value.(List)
	default:
		return nil, errors.New("bad attributes")
	}
	attrs := make([]Attribute, 0, list.Size)
	for i := 0; i < list.Size; i++ {
		key, err := parseString(decoder)
		if err != nil {
			return nil, err
		}
		value, err := parseString(decoder)
		if err != nil {
			return nil, err
		}
		attrs = append(attrs, Attribute{Key: key, Value: value})
	}
	return attrs, nil
}

func parseContent(decoder *Decoder) ([]*Node, error) {
	token, err := peekToken(decoder)
	if err != nil {
		return nil, err
	}
	if token.Type == TypeListEnd {
		consumeToken(decoder)
		return nil, nil
	}
	var nodes []*Node
	for {
		tok, err := peekToken(decoder)
		if err != nil {
			return nil, err
		}
		if tok.Type == TypeListEnd {
			consumeToken(decoder)
			break
		}
		n, err := parseSingleContent(decoder)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

func parseSingleContent(decoder *Decoder) (*Node, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token.Type {
	case TypeBinary, TypeStringRaw:
		return &Node{Tag: token.GetString()}, nil
	case TypePacked:
		values := token.Value.([]interface{})
		s := ""
		for _, v := range values {
			s += fmt.Sprint(v)
		}
		return &Node{Tag: s}, nil
	case TypeList, TypeDictionary:
		return parseNodeFromToken(token, decoder)
	case TypeSubStart:
		return parseNode(decoder)
	default:
		return nil, fmt.Errorf("unexpected token in content: %v", token.Type)
	}
}

func parseNodeFromToken(token *Token, decoder *Decoder) (*Node, error) {
	if token.Type == TypeDictionary {
		dict := token.Value.(List)
		return parseDictionary(dict, decoder)
	}
	list := token.Value.(List)
	return parseList(list, decoder)
}

type peekable struct {
	pending *Token
}

var peekState = map[*Decoder]*peekable{}

func peekToken(decoder *Decoder) (*Token, error) {
	p := peekState[decoder]
	if p == nil {
		p = &peekable{}
		peekState[decoder] = p
	}
	if p.pending != nil {
		return p.pending, nil
	}
	tok, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	p.pending = tok
	return tok, nil
}

func consumeToken(decoder *Decoder) {
	p := peekState[decoder]
	if p != nil {
		p.pending = nil
	}
}

func parseString(decoder *Decoder) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	switch token.Type {
	case TypeBinary, TypeStringRaw:
		return token.GetString(), nil
	case TypeListEnd:
		return "", nil
	case TypePacked:
		values := token.Value.([]interface{})
		s := ""
		for _, v := range values {
			s += fmt.Sprint(v)
		}
		return s, nil
	default:
		return "", fmt.Errorf("expected string, got %v", token.Type)
	}
}

package proto

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

type Attribute struct {
	Key   string
	Value string
}

func (a Attribute) GetInt() int {
	i, _ := strconv.Atoi(a.Value)
	return i
}

func (a Attribute) IsDefault() bool {
	return a.Key == "" && a.Value == ""
}

type Node struct {
	Tag      string
	Attrs    []Attribute
	Content  []*Node
	UserData interface{}
}

func NewNode(tag string, attrs ...Attribute) *Node {
	return &Node{Tag: tag, Attrs: attrs}
}

func (n *Node) AddChild(nodes ...*Node) {
	n.Content = append(n.Content, nodes...)
}

func (n *Node) GetChild(tag string) *Node {
	for _, c := range n.Content {
		if c.Tag == tag {
			return c
		}
	}
	return nil
}

func (n *Node) Query(tag, attrKey, attrValue string) *Node {
	for _, c := range n.Content {
		if c.Tag != tag {
			continue
		}
		if attrKey == "" && attrValue == "" {
			return c
		}
		for _, a := range c.Attrs {
			if a.Key == attrKey && a.Value == attrValue {
				return c
			}
		}
	}
	return nil
}

func (n *Node) FindAttr(key string) (Attribute, bool) {
	for _, a := range n.Attrs {
		if a.Key == key {
			return a, true
		}
	}
	return Attribute{}, false
}

func (n *Node) GetDataBytes() []byte {
	data := n.GetChild("data")
	if data == nil || len(data.Content) == 0 {
		return nil
	}
	raw := data.Content[0].Tag
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil
	}
	return b
}

func (n *Node) SetData(content []byte) {
	child := &Node{
		Tag:   "data",
		Attrs: []Attribute{{Key: "encoding", Value: "base64"}},
		Content: []*Node{
			{Tag: base64.StdEncoding.EncodeToString(content)},
		},
	}
	n.AddChild(child)
}

var attrEscaper = strings.NewReplacer(
	`&`, `&amp;`,
	`<`, `&lt;`,
	`>`, `&gt;`,
	`"`, `&quot;`,
	`'`, `&apos;`,
)

var textEscaper = strings.NewReplacer(
	`&`, `&amp;`,
	`<`, `&lt;`,
	`>`, `&gt;`,
)

var attrUnescaper = strings.NewReplacer(
	`&quot;`, `"`,
	`&apos;`, `'`,
	`&lt;`, `<`,
	`&gt;`, `>`,
	`&amp;`, `&`,
)

var textUnescaper = strings.NewReplacer(
	`&lt;`, `<`,
	`&gt;`, `>`,
	`&amp;`, `&`,
)

func (n *Node) MarshalXML() string {
	var sb strings.Builder
	writeNode(&sb, n)
	return sb.String()
}

func writeNode(sb *strings.Builder, n *Node) {
	if isTextLeaf(n) {
		sb.WriteString(textEscaper.Replace(n.Tag))
		return
	}
	sb.WriteString("<")
	sb.WriteString(n.Tag)
	for _, a := range n.Attrs {
		sb.WriteString(" ")
		sb.WriteString(a.Key)
		sb.WriteString(`="`)
		sb.WriteString(attrEscaper.Replace(a.Value))
		sb.WriteString(`"`)
	}
	if len(n.Content) == 0 {
		sb.WriteString("/>")
		return
	}
	sb.WriteString(">")
	for _, c := range n.Content {
		writeNode(sb, c)
	}
	sb.WriteString("</")
	sb.WriteString(n.Tag)
	sb.WriteString(">")
}

func isTextLeaf(n *Node) bool {
	return len(n.Content) == 0 && len(n.Attrs) == 0 && !isTagName(n.Tag)
}

func isTagName(s string) bool {
	if s == "" {
		return false
	}
	return !strings.ContainsAny(s, " <>\"'=&")
}

func Unmarshal(data []byte) (*Node, error) {
	p := &parser{src: string(data)}
	node, err := p.parseDocument()
	if err != nil {
		return nil, fmt.Errorf("bad xml: %w", err)
	}
	return node, nil
}

type parser struct {
	src string
	pos int
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			p.pos++
			continue
		}
		break
	}
}

func (p *parser) parseDocument() (*Node, error) {
	p.skipSpace()
	if p.pos >= len(p.src) || p.src[p.pos] != '<' {
		return nil, fmt.Errorf("expected < at %d", p.pos)
	}
	return p.parseElement()
}

func (p *parser) parseElement() (*Node, error) {
	p.pos++
	nameStart := p.pos
	for p.pos < len(p.src) && p.src[p.pos] != ' ' && p.src[p.pos] != '>' && p.src[p.pos] != '/' {
		p.pos++
	}
	tag := p.src[nameStart:p.pos]
	attrs, err := p.parseAttributes()
	if err != nil {
		return nil, err
	}
	node := &Node{Tag: tag, Attrs: attrs}
	if p.pos >= len(p.src) {
		return nil, fmt.Errorf("unexpected end in element %s", tag)
	}
	if p.src[p.pos] == '/' {
		p.pos++
		if p.pos >= len(p.src) || p.src[p.pos] != '>' {
			return nil, fmt.Errorf("expected > after /")
		}
		p.pos++
		return node, nil
	}
	p.pos++
	for p.pos < len(p.src) {
		if p.src[p.pos] == '<' {
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '/' {
				end := strings.IndexByte(p.src[p.pos:], '>')
				if end < 0 {
					return nil, fmt.Errorf("unclosed element %s", tag)
				}
				p.pos += end + 1
				return node, nil
			}
			child, err := p.parseElement()
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, child)
			continue
		}
		textEnd := strings.IndexByte(p.src[p.pos:], '<')
		if textEnd < 0 {
			return nil, fmt.Errorf("unclosed element %s", tag)
		}
		text := textUnescaper.Replace(p.src[p.pos : p.pos+textEnd])
		if text != "" {
			node.Content = append(node.Content, &Node{Tag: text})
		}
		p.pos += textEnd
	}
	return nil, fmt.Errorf("unexpected end of input")
}

func (p *parser) parseAttributes() ([]Attribute, error) {
	var attrs []Attribute
	for {
		p.skipSpace()
		if p.pos >= len(p.src) {
			return nil, fmt.Errorf("unexpected end in attributes")
		}
		if p.src[p.pos] == '>' || p.src[p.pos] == '/' {
			return attrs, nil
		}
		keyStart := p.pos
		for p.pos < len(p.src) && p.src[p.pos] != '=' && p.src[p.pos] != ' ' && p.src[p.pos] != '>' {
			p.pos++
		}
		key := p.src[keyStart:p.pos]
		if key == "" {
			return nil, fmt.Errorf("empty attribute name at %d", p.pos)
		}
		p.skipSpace()
		if p.pos >= len(p.src) || p.src[p.pos] != '=' {
			return nil, fmt.Errorf("expected = for attribute %s", key)
		}
		p.pos++
		p.skipSpace()
		if p.pos >= len(p.src) || (p.src[p.pos] != '"' && p.src[p.pos] != '\'') {
			return nil, fmt.Errorf("expected quote for attribute %s", key)
		}
		quote := p.src[p.pos]
		p.pos++
		valStart := p.pos
		for p.pos < len(p.src) && p.src[p.pos] != quote {
			p.pos++
		}
		value := attrUnescaper.Replace(p.src[valStart:p.pos])
		if p.pos >= len(p.src) {
			return nil, fmt.Errorf("unterminated attribute %s", key)
		}
		p.pos++
		attrs = append(attrs, Attribute{Key: key, Value: value})
	}
}

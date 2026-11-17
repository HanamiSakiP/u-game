// Package vdf 解析 Valve KeyValues 文本格式（libraryfolders.vdf、appmanifest_*.acf）。
package vdf

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Node 是 KeyValues 树中的一个节点。叶子节点的 Value 有值，对象节点的 Children 有值。
type Node struct {
	Key      string
	Value    string
	Children []*Node
}

// HasChildren 报告节点是否为对象节点。
func (n *Node) HasChildren() bool { return n != nil && len(n.Children) > 0 }

// Child 按名称查找直接子节点（不区分大小写）。
func (n *Node) Child(key string) *Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if strings.EqualFold(c.Key, key) {
			return c
		}
	}
	return nil
}

// ChildrenNamed 返回所有名称匹配的子节点（不区分大小写）。
func (n *Node) ChildrenNamed(key string) []*Node {
	if n == nil {
		return nil
	}
	var out []*Node
	for _, c := range n.Children {
		if strings.EqualFold(c.Key, key) {
			out = append(out, c)
		}
	}
	return out
}

// String 返回叶子节点的值；对象节点返回空串。
func (n *Node) String() string {
	if n == nil {
		return ""
	}
	return n.Value
}

// Int 将叶子节点的值解析为整数。
func (n *Node) Int() int {
	if n == nil {
		return 0
	}
	v, err := strconv.Atoi(strings.TrimSpace(n.Value))
	if err != nil {
		return 0
	}
	return v
}

// Int64 将叶子节点的值解析为 64 位整数。
func (n *Node) Int64() int64 {
	if n == nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(n.Value), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// ParseFile 读取并解析一个 VDF/ACF 文件。
func ParseFile(path string) (*Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	node, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return node, nil
}

// Parse 解析 VDF/ACF 文本，返回第一个顶层对象。
func Parse(data []byte) (*Node, error) {
	l := &lexer{src: data, line: 1}
	tok, err := l.next()
	if err != nil {
		return nil, err
	}
	if tok.kind != tokString {
		return nil, l.errf("期望顶层键名，得到 %q", tok.text)
	}
	key := tok.text

	tok, err = l.next()
	if err != nil {
		return nil, err
	}
	if tok.kind != tokLBrace {
		return nil, l.errf("键 %q 后缺少 '{'", key)
	}
	return l.parseObject(key)
}

func (l *lexer) parseObject(name string) (*Node, error) {
	node := &Node{Key: name}
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		switch tok.kind {
		case tokRBrace:
			return node, nil
		case tokEOF:
			return nil, l.errf("对象 %q 未闭合", name)
		case tokString:
			key := tok.text
			val, err := l.next()
			if err != nil {
				return nil, err
			}
			switch val.kind {
			case tokLBrace:
				child, err := l.parseObject(key)
				if err != nil {
					return nil, err
				}
				node.Children = append(node.Children, child)
			case tokString:
				node.Children = append(node.Children, &Node{Key: key, Value: val.text})
			default:
				return nil, l.errf("键 %q 的值无效", key)
			}
		default:
			return nil, l.errf("对象 %q 中出现了意外的 %q", name, tok.text)
		}
	}
}

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokString
	tokLBrace
	tokRBrace
)

type token struct {
	kind tokenKind
	text string
}

type lexer struct {
	src  []byte
	pos  int
	line int
}

func (l *lexer) errf(format string, args ...interface{}) error {
	return fmt.Errorf("第 %d 行: %s", l.line, fmt.Sprintf(format, args...))
}

func (l *lexer) next() (token, error) {
	l.skipSpaceAndComments()
	if l.pos >= len(l.src) {
		return token{kind: tokEOF}, nil
	}
	switch c := l.src[l.pos]; c {
	case '{':
		l.pos++
		return token{kind: tokLBrace, text: "{"}, nil
	case '}':
		l.pos++
		return token{kind: tokRBrace, text: "}"}, nil
	case '"':
		return l.quoted()
	default:
		return l.bare(), nil
	}
}

func (l *lexer) skipSpaceAndComments() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			if c == '\n' {
				l.line++
			}
			l.pos++
		case c == 0xEF && l.pos+2 < len(l.src) && l.src[l.pos+1] == 0xBB && l.src[l.pos+2] == 0xBF:
			// UTF-8 BOM
			l.pos += 3
		case c == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
		default:
			return
		}
	}
}

func (l *lexer) quoted() (token, error) {
	l.pos++ // 跳过起始引号
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch c {
		case '"':
			l.pos++
			return token{kind: tokString, text: b.String()}, nil
		case '\\':
			l.pos++
			if l.pos >= len(l.src) {
				return token{}, l.errf("字符串末尾出现转义符")
			}
			e := l.src[l.pos]
			l.pos++
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\', '"':
				b.WriteByte(e)
			default:
				// Valve 的路径转义通常只使用 \\，其余情况原样保留。
				b.WriteByte(e)
			}
		case '\n':
			l.line++
			b.WriteByte(c)
			l.pos++
		default:
			b.WriteByte(c)
			l.pos++
		}
	}
	return token{}, l.errf("字符串未闭合")
}

func (l *lexer) bare() token {
	start := l.pos
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '{' || c == '}' {
			break
		}
		l.pos++
	}
	return token{kind: tokString, text: string(l.src[start:l.pos])}
}

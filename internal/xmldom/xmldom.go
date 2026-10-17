// Package xmldom is a tiny DOM over encoding/xml keyed by local names, which
// keeps OOXML traversal readable without generating structs for every schema.
package xmldom

import (
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

const relNS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"

type Node struct {
	Name     string
	Attrs    map[string]string
	Children []*Node
	Text     string
}

// Parse builds a tree. Relationship-namespace attributes are stored as "r:<local>".
func Parse(data []byte) (*Node, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var stack []*Node
	var root *Node
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &Node{Name: t.Name.Local, Attrs: make(map[string]string, len(t.Attr))}
			for _, a := range t.Attr {
				key := a.Name.Local
				if a.Name.Space == relNS {
					key = "r:" + key
				}
				n.Attrs[key] = a.Value
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.Children = append(p.Children, n)
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(t)
			}
		}
	}
	if root == nil {
		return nil, io.ErrUnexpectedEOF
	}
	return root, nil
}

// Child returns the first direct child with the given local name.
func (n *Node) Child(name string) *Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Path walks direct children by slash-separated local names.
func (n *Node) Path(path string) *Node {
	cur := n
	for _, p := range strings.Split(path, "/") {
		cur = cur.Child(p)
		if cur == nil {
			return nil
		}
	}
	return cur
}

func (n *Node) All(name string) []*Node {
	if n == nil {
		return nil
	}
	var out []*Node
	for _, c := range n.Children {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// Find returns all descendants (depth-first) with the given local name.
func (n *Node) Find(name string) []*Node {
	if n == nil {
		return nil
	}
	var out []*Node
	var walk func(*Node)
	walk = func(x *Node) {
		for _, c := range x.Children {
			if c.Name == name {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(n)
	return out
}

func (n *Node) Attr(name string) string {
	if n == nil {
		return ""
	}
	return n.Attrs[name]
}

func (n *Node) Int(name string) (int64, bool) {
	v := n.Attr(name)
	if v == "" {
		return 0, false
	}
	i, err := strconv.ParseInt(v, 10, 64)
	return i, err == nil
}

func (n *Node) Bool(name string) (bool, bool) {
	v := n.Attr(name)
	switch v {
	case "1", "true", "on":
		return true, true
	case "0", "false", "off":
		return false, true
	}
	return false, false
}

package doc

import (
	"crypto/rand"
	"strings"
)

// Shape order is kept in fractional indices, strings that sort between any
// two others, which is what .tldr files store: a letter whose distance from
// 'a' (upward) or 'Z' (downward) gives the length of the integer part, then
// the digits of the integer part, then a fraction.

const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// FirstIndex is the index of a first child.
const FirstIndex = "a1"

func intLen(head byte) int {
	switch {
	case head >= 'a' && head <= 'z':
		return int(head-'a') + 2
	case head >= 'A' && head <= 'Z':
		return int('Z'-head) + 2
	}
	return 0
}

func intPart(key string) string {
	if key == "" {
		return ""
	}
	n := intLen(key[0])
	if n == 0 || n > len(key) {
		return key
	}
	return key[:n]
}

func incInt(x string) string {
	d := []byte(x)
	head := d[0]
	body := d[1:]
	carry := true
	for i := len(body) - 1; carry && i >= 0; i-- {
		v := strings.IndexByte(digits, body[i]) + 1
		if v == len(digits) {
			body[i] = '0'
		} else {
			body[i] = digits[v]
			carry = false
		}
	}
	if !carry {
		return string(d)
	}
	switch {
	case head == 'Z':
		return "a0"
	case head == 'z':
		return "" // out of range
	}
	head++
	if head > 'a' {
		body = append(body, '0')
	} else {
		body = body[:len(body)-1]
	}
	return string(head) + string(body)
}

func decInt(x string) string {
	d := []byte(x)
	head := d[0]
	body := d[1:]
	borrow := true
	for i := len(body) - 1; borrow && i >= 0; i-- {
		v := strings.IndexByte(digits, body[i]) - 1
		if v == -1 {
			body[i] = digits[len(digits)-1]
		} else {
			body[i] = digits[v]
			borrow = false
		}
	}
	if !borrow {
		return string(d)
	}
	switch {
	case head == 'a':
		return "Z" + string(digits[len(digits)-1])
	case head == 'A':
		return "" // out of range
	}
	head--
	if head < 'Z' {
		body = append(body, digits[len(digits)-1])
	} else {
		body = body[:len(body)-1]
	}
	return string(head) + string(body)
}

// midpoint returns a fraction strictly between a and b; b empty is the top.
func midpoint(a, b string) string {
	if b != "" && a >= b {
		return a + "V" // malformed order: keep something usable
	}
	if b != "" {
		n := 0
		for n < len(b) {
			ca := byte('0')
			if n < len(a) {
				ca = a[n]
			}
			if ca != b[n] {
				break
			}
			n++
		}
		if n > 0 {
			rest := ""
			if n < len(a) {
				rest = a[n:]
			}
			return b[:n] + midpoint(rest, b[n:])
		}
	}
	da := 0
	if a != "" {
		da = strings.IndexByte(digits, a[0])
	}
	db := len(digits)
	if b != "" {
		db = strings.IndexByte(digits, b[0])
	}
	if db-da > 1 {
		return string(digits[(da+db+1)/2])
	}
	if b != "" && len(b) > 1 {
		return b[:1]
	}
	rest := ""
	if len(a) > 1 {
		rest = a[1:]
	}
	return string(digits[da]) + midpoint(rest, "")
}

// IndexBetween returns an index that sorts between a and b, either of
// which may be empty for no bound.
func IndexBetween(a, b string) string {
	switch {
	case a == "" && b == "":
		return FirstIndex
	case a == "":
		ib := intPart(b)
		fb := b[len(ib):]
		if ib == "A"+strings.Repeat("0", 26) {
			return ib + midpoint("", fb)
		}
		if ib < b {
			return ib
		}
		if d := decInt(ib); d != "" {
			return d
		}
		return ib + midpoint("", fb)
	case b == "":
		ia := intPart(a)
		fa := a[len(ia):]
		if i := incInt(ia); i != "" {
			return i
		}
		return ia + midpoint(fa, "")
	}
	ia, ib := intPart(a), intPart(b)
	fa, fb := a[len(ia):], b[len(ib):]
	if ia == ib {
		return ia + midpoint(fa, fb)
	}
	i := incInt(ia)
	if i != "" && i < b {
		return i
	}
	return ia + midpoint(fa, "")
}

// IndicesBetween returns n indices in order between a and b.
func IndicesBetween(a, b string, n int) []string {
	out := make([]string, 0, n)
	cur := a
	for i := 0; i < n; i++ {
		if b != "" && i == n-1 {
			cur = IndexBetween(cur, b)
		} else if b != "" {
			// Split the room that is left, so that many indices stay short.
			cur = IndexBetween(cur, b)
		} else {
			cur = IndexBetween(cur, "")
		}
		out = append(out, cur)
	}
	return out
}

// NewID returns a new id of a kind of record, as "shape:…".
func NewID(kind string) ID {
	const alpha = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	var b [21]byte
	rand.Read(b[:])
	for i := range b {
		b[i] = alpha[int(b[i])%len(alpha)]
	}
	return ID(kind + ":" + string(b[:]))
}

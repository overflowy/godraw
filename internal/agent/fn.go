package agent

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"godraw/internal/geo"
)

// A draw can say a curve as a formula, so that a wave or a bell needn't be worked
// out elsewhere and pasted in as a long path: page y as a function of page x.

const (
	maxFnLen   = 2000
	maxFnDepth = 64
	// How far apart a curve's points are, in the page, unless that makes too few or
	// too many of them.
	fnStep    = 2.0
	minFnStep = 64
	maxFnStep = 5000
)

// plot returns the curve of a formula from x1 to x2. Where it has no value, as 1/x
// at 0, it breaks into pieces.
func plot(src string, x1, x2 float64) (*geo.Path, error) {
	if x1 == x2 {
		return nil, errors.New("fn needs x1 and x2 apart")
	}
	eval, err := parseFn(src)
	if err != nil {
		return nil, fmt.Errorf("fn: %w", err)
	}
	n := max(min(int(math.Abs(x2-x1)/fnStep), maxFnStep), minFnStep)
	p := &geo.Path{}
	down := false
	drawn := 0
	for i := 0; i <= n; i++ {
		x := x1 + (x2-x1)*float64(i)/float64(n)
		y := eval(x)
		if math.IsNaN(y) || !inReach(y) {
			down = false
			continue
		}
		if down {
			p.Line(geo.V(x, y))
			drawn++
		} else {
			p.Move(geo.V(x, y))
			down = true
		}
	}
	if drawn == 0 {
		return nil, fmt.Errorf("fn has no values to draw from x=%s to x=%s", num(x1), num(x2))
	}
	return p, nil
}

// fnParser reads a formula by recursive descent, into a function of x.
type fnParser struct {
	src   string
	at    int
	depth int
}

func parseFn(src string) (func(float64) float64, error) {
	if len(src) > maxFnLen {
		return nil, fmt.Errorf("it's over %d characters", maxFnLen)
	}
	p := &fnParser{src: src}
	f, err := p.sum()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.at < len(src) {
		return nil, fmt.Errorf("%q at %d is not expected", src[p.at:p.at+1], p.at)
	}
	return f, nil
}

func (p *fnParser) space() {
	for p.at < len(p.src) && unicode.IsSpace(rune(p.src[p.at])) {
		p.at++
	}
}

// next says what comes next, without taking it.
func (p *fnParser) next() byte {
	p.space()
	if p.at < len(p.src) {
		return p.src[p.at]
	}
	return 0
}

func (p *fnParser) sum() (func(float64) float64, error) {
	left, err := p.product()
	if err != nil {
		return nil, err
	}
	for {
		op := p.next()
		if op != '+' && op != '-' {
			return left, nil
		}
		p.at++
		right, err := p.product()
		if err != nil {
			return nil, err
		}
		a := left
		if op == '+' {
			left = func(x float64) float64 { return a(x) + right(x) }
		} else {
			left = func(x float64) float64 { return a(x) - right(x) }
		}
	}
}

func (p *fnParser) product() (func(float64) float64, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		op := p.next()
		if op != '*' && op != '/' && op != '%' {
			return left, nil
		}
		p.at++
		right, err := p.unary()
		if err != nil {
			return nil, err
		}
		a := left
		switch op {
		case '*':
			left = func(x float64) float64 { return a(x) * right(x) }
		case '/':
			left = func(x float64) float64 { return a(x) / right(x) }
		default:
			left = func(x float64) float64 { return math.Mod(a(x), right(x)) }
		}
	}
}

// unary takes a sign, which binds looser than a power: -x^2 is -(x^2).
func (p *fnParser) unary() (func(float64) float64, error) {
	switch p.next() {
	case '-':
		p.at++
		f, err := p.nested(p.unary)
		if err != nil {
			return nil, err
		}
		return func(x float64) float64 { return -f(x) }, nil
	case '+':
		p.at++
		return p.nested(p.unary)
	}
	return p.power()
}

// nested goes a level deeper, as far as is sensible.
func (p *fnParser) nested(in func() (func(float64) float64, error)) (func(float64) float64, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxFnDepth {
		return nil, errors.New("it nests too deep")
	}
	return in()
}

func (p *fnParser) power() (func(float64) float64, error) {
	base, err := p.atom()
	if err != nil {
		return nil, err
	}
	if p.next() != '^' {
		return base, nil
	}
	p.at++
	exp, err := p.nested(p.unary) // 2^-x, and 2^3^2 is 2^(3^2)
	if err != nil {
		return nil, err
	}
	return func(x float64) float64 { return math.Pow(base(x), exp(x)) }, nil
}

func (p *fnParser) atom() (func(float64) float64, error) {
	c := p.next()
	switch {
	case c == 0:
		return nil, errors.New("it ends too soon")
	case c == '(':
		p.at++
		f, err := p.nested(p.sum)
		if err != nil {
			return nil, err
		}
		if p.next() != ')' {
			return nil, fmt.Errorf("a ) is missing at %d", p.at)
		}
		p.at++
		return f, nil
	case c >= '0' && c <= '9' || c == '.':
		return p.number()
	case unicode.IsLetter(rune(c)):
		return p.name()
	}
	return nil, fmt.Errorf("%q at %d is not expected", string(c), p.at)
}

func (p *fnParser) number() (func(float64) float64, error) {
	start := p.at
	for p.at < len(p.src) {
		c := p.src[p.at]
		exponent := (c == 'e' || c == 'E') && p.at+1 < len(p.src) && strings.ContainsRune("0123456789+-", rune(p.src[p.at+1]))
		sign := (c == '+' || c == '-') && (p.src[p.at-1] == 'e' || p.src[p.at-1] == 'E')
		digit := c >= '0' && c <= '9'
		if !digit && c != '.' && !exponent && !sign {
			break
		}
		p.at++
	}
	v, err := strconv.ParseFloat(p.src[start:p.at], 64)
	if err != nil {
		return nil, fmt.Errorf("%q is not a number", p.src[start:p.at])
	}
	return func(float64) float64 { return v }, nil
}

var fnConstants = map[string]float64{"pi": math.Pi, "tau": 2 * math.Pi, "e": math.E}

var fnOne = map[string]func(float64) float64{
	"sin": math.Sin, "cos": math.Cos, "tan": math.Tan, "asin": math.Asin, "acos": math.Acos, "atan": math.Atan,
	"sinh": math.Sinh, "cosh": math.Cosh, "tanh": math.Tanh, "exp": math.Exp, "log": math.Log, "log10": math.Log10,
	"sqrt": math.Sqrt, "abs": math.Abs, "floor": math.Floor, "ceil": math.Ceil, "round": math.Round,
	"sign": func(v float64) float64 {
		switch {
		case v > 0:
			return 1
		case v < 0:
			return -1
		}
		return v
	},
}

var fnTwo = map[string]func(a, b float64) float64{
	"min": math.Min, "max": math.Max, "pow": math.Pow, "atan2": math.Atan2,
}

// fnNames lists what a formula can call, for an error to say.
const fnNames = "x pi tau e sin cos tan asin acos atan sinh cosh tanh exp log log10 sqrt abs floor ceil round sign min max pow atan2 gauss"

func (p *fnParser) name() (func(float64) float64, error) {
	start := p.at
	for p.at < len(p.src) && (unicode.IsLetter(rune(p.src[p.at])) || unicode.IsDigit(rune(p.src[p.at]))) {
		p.at++
	}
	name := p.src[start:p.at]
	if p.next() != '(' {
		if name == "x" {
			return func(x float64) float64 { return x }, nil
		}
		if v, ok := fnConstants[name]; ok {
			return func(float64) float64 { return v }, nil
		}
		return nil, fmt.Errorf("%q isn't known; use %s", name, fnNames)
	}
	p.at++
	var args []func(float64) float64
	for {
		arg, err := p.nested(p.sum)
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if p.next() != ',' {
			break
		}
		p.at++
	}
	if p.next() != ')' {
		return nil, fmt.Errorf("a ) is missing at %d", p.at)
	}
	p.at++
	if f, ok := fnOne[name]; ok {
		if len(args) != 1 {
			return nil, fmt.Errorf("%s takes 1 value", name)
		}
		a := args[0]
		return func(x float64) float64 { return f(a(x)) }, nil
	}
	if f, ok := fnTwo[name]; ok {
		if len(args) != 2 {
			return nil, fmt.Errorf("%s takes 2 values", name)
		}
		a, b := args[0], args[1]
		return func(x float64) float64 { return f(a(x), b(x)) }, nil
	}
	if name == "gauss" {
		// The bell of height 1 at c, as wide as s: gauss(x,c,s).
		if len(args) != 3 {
			return nil, errors.New("gauss takes 3 values: gauss(x,center,width)")
		}
		a, c, s := args[0], args[1], args[2]
		return func(x float64) float64 {
			d := (a(x) - c(x)) / s(x)
			return math.Exp(-d * d / 2)
		}, nil
	}
	return nil, fmt.Errorf("%q isn't known; use %s", name, fnNames)
}

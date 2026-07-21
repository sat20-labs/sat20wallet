package fuzzy

import "fmt"

func mod(value, prime int64) int64 {
	value %= prime
	if value < 0 {
		value += prime
	}
	return value
}
func modAdd(a, b, p int64) int64 { return mod(a+b, p) }
func modSub(a, b, p int64) int64 { return mod(a-b, p) }
func modMul(a, b, p int64) int64 { return mod(a*b, p) }
func modPow(a, e, p int64) int64 {
	r := int64(1)
	a = mod(a, p)
	for e > 0 {
		if e&1 == 1 {
			r = modMul(r, a, p)
		}
		a = modMul(a, a, p)
		e >>= 1
	}
	return r
}
func modInv(a, p int64) (int64, error) {
	a = mod(a, p)
	if a == 0 {
		return 0, fmt.Errorf("division by zero")
	}
	return modPow(a, p-2, p), nil
}
func isPrime(n int64) bool {
	if n < 2 {
		return false
	}
	if n == 2 || n == 3 {
		return true
	}
	if n%2 == 0 || n%3 == 0 {
		return false
	}
	for i := int64(5); i*i <= n; i += 6 {
		if n%i == 0 || n%(i+2) == 0 {
			return false
		}
	}
	return true
}
func firstPrimeGreaterThan(n int) (int64, error) {
	if n < 1 || n >= 1_000_000 {
		return 0, ErrInvalidParameters
	}
	for p := int64(n + 1); p <= 1_000_003; p++ {
		if isPrime(p) {
			return p, nil
		}
	}
	return 0, ErrInvalidParameters
}

type polynomial struct {
	prime  int64
	coeffs []int64
}

func newPolynomial(p int64, c []int64) polynomial {
	out := append([]int64(nil), c...)
	for i := range out {
		out[i] = mod(out[i], p)
	}
	q := polynomial{p, out}
	q.trim()
	return q
}
func (p *polynomial) trim() {
	for len(p.coeffs) > 0 && p.coeffs[len(p.coeffs)-1] == 0 {
		p.coeffs = p.coeffs[:len(p.coeffs)-1]
	}
}
func (p polynomial) degree() int { return len(p.coeffs) - 1 }
func (p polynomial) eval(x int64) int64 {
	r := int64(0)
	for i := len(p.coeffs) - 1; i >= 0; i-- {
		r = modAdd(modMul(r, x, p.prime), p.coeffs[i], p.prime)
	}
	return r
}
func (p polynomial) sub(q polynomial) polynomial {
	n := len(p.coeffs)
	if len(q.coeffs) > n {
		n = len(q.coeffs)
	}
	c := make([]int64, n)
	for i := 0; i < n; i++ {
		var a, b int64
		if i < len(p.coeffs) {
			a = p.coeffs[i]
		}
		if i < len(q.coeffs) {
			b = q.coeffs[i]
		}
		c[i] = modSub(a, b, p.prime)
	}
	return newPolynomial(p.prime, c)
}
func (p polynomial) mul(q polynomial) polynomial {
	if p.degree() < 0 || q.degree() < 0 {
		return newPolynomial(p.prime, nil)
	}
	c := make([]int64, len(p.coeffs)+len(q.coeffs)-1)
	for i, a := range p.coeffs {
		for j, b := range q.coeffs {
			c[i+j] = modAdd(c[i+j], modMul(a, b, p.prime), p.prime)
		}
	}
	return newPolynomial(p.prime, c)
}
func polyFromRoots(roots []int64, p int64) polynomial {
	q := newPolynomial(p, []int64{1})
	for _, r := range roots {
		q = q.mul(newPolynomial(p, []int64{-r, 1}))
	}
	return q
}
func divRemPolynomial(n, d polynomial) (polynomial, polynomial, error) {
	if d.degree() < 0 {
		return polynomial{}, polynomial{}, fmt.Errorf("divide by zero polynomial")
	}
	if n.degree() < d.degree() {
		return newPolynomial(n.prime, nil), n, nil
	}
	q := make([]int64, n.degree()-d.degree()+1)
	r := newPolynomial(n.prime, n.coeffs)
	inv, err := modInv(d.coeffs[d.degree()], n.prime)
	if err != nil {
		return polynomial{}, polynomial{}, err
	}
	for r.degree() >= d.degree() {
		shift := r.degree() - d.degree()
		factor := modMul(r.coeffs[r.degree()], inv, n.prime)
		q[shift] = factor
		for i := 0; i <= d.degree(); i++ {
			idx := i + shift
			r.coeffs[idx] = modSub(r.coeffs[idx], modMul(factor, d.coeffs[i], n.prime), n.prime)
		}
		r.trim()
	}
	return newPolynomial(n.prime, q), r, nil
}
func findRoots(p polynomial, expected int) ([]int64, error) {
	if p.degree() != expected {
		return nil, ErrInsufficientMatches
	}
	roots := make([]int64, 0, expected)
	for x := int64(0); x < p.prime; x++ {
		if p.eval(x) == 0 {
			roots = append(roots, x)
		}
	}
	if len(roots) != expected {
		return nil, ErrInsufficientMatches
	}
	return roots, nil
}
func powers(x int64, n int, p int64) []int64 {
	out := make([]int64, n)
	if n == 0 {
		return out
	}
	out[0] = 1
	for i := 1; i < n; i++ {
		out[i] = modMul(out[i-1], x, p)
	}
	return out
}

func solveLinear(a [][]int64, b []int64, p int64) ([]int64, error) {
	rows := len(a)
	if rows == 0 || len(b) != rows {
		return nil, ErrInsufficientMatches
	}
	cols := len(a[0])
	aug := make([][]int64, rows)
	for i := range a {
		if len(a[i]) != cols {
			return nil, ErrInsufficientMatches
		}
		aug[i] = make([]int64, cols+1)
		for j := 0; j < cols; j++ {
			aug[i][j] = mod(a[i][j], p)
		}
		aug[i][cols] = mod(b[i], p)
	}
	pivotRow := 0
	pivots := make([]int, 0, cols)
	for col := 0; col < cols && pivotRow < rows; col++ {
		sel := -1
		for r := pivotRow; r < rows; r++ {
			if aug[r][col] != 0 {
				sel = r
				break
			}
		}
		if sel < 0 {
			continue
		}
		aug[pivotRow], aug[sel] = aug[sel], aug[pivotRow]
		inv, err := modInv(aug[pivotRow][col], p)
		if err != nil {
			return nil, ErrInsufficientMatches
		}
		for j := col; j <= cols; j++ {
			aug[pivotRow][j] = modMul(aug[pivotRow][j], inv, p)
		}
		for r := 0; r < rows; r++ {
			if r == pivotRow {
				continue
			}
			f := aug[r][col]
			if f == 0 {
				continue
			}
			for j := col; j <= cols; j++ {
				aug[r][j] = modSub(aug[r][j], modMul(f, aug[pivotRow][j], p), p)
			}
		}
		pivots = append(pivots, col)
		pivotRow++
	}
	for r := 0; r < rows; r++ {
		allZero := true
		for c := 0; c < cols; c++ {
			if aug[r][c] != 0 {
				allZero = false
				break
			}
		}
		if allZero && aug[r][cols] != 0 {
			return nil, ErrInsufficientMatches
		}
	}
	x := make([]int64, cols)
	for row, col := range pivots {
		x[col] = aug[row][cols]
	}
	return x, nil
}

func berlekampWelch(xs, ys []int64, k, errors int, p int64) (polynomial, error) {
	n := len(xs)
	if n == 0 || n != len(ys) || k <= 0 || errors <= 0 || n != k+2*errors {
		return polynomial{}, ErrInsufficientMatches
	}
	qCount := k + errors
	m := make([][]int64, n)
	rhs := make([]int64, n)
	for i := 0; i < n; i++ {
		m[i] = make([]int64, n)
		ap := powers(xs[i], qCount, p)
		copy(m[i][:qCount], ap)
		ep := powers(xs[i], errors+1, p)
		for j := 0; j < errors; j++ {
			m[i][qCount+j] = mod(-modMul(ys[i], ep[j], p), p)
		}
		rhs[i] = modMul(ys[i], ep[errors], p)
	}
	solution, err := solveLinear(m, rhs, p)
	if err != nil {
		return polynomial{}, err
	}
	q := newPolynomial(p, solution[:qCount])
	ec := make([]int64, errors+1)
	copy(ec, solution[qCount:])
	ec[errors] = 1
	e := newPolynomial(p, ec)
	quotient, remainder, err := divRemPolynomial(q, e)
	if err != nil || remainder.degree() >= 0 {
		return polynomial{}, ErrInsufficientMatches
	}
	return quotient, nil
}

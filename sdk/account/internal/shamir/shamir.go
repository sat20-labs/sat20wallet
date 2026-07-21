// Package shamir implements Shamir secret sharing over GF(2^8).
// Share coordinates are public and fixed to 1..N; polynomial coefficients are
// generated from a cryptographically secure random source.
package shamir

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"io"
)

const ShareOverhead = 1

type polynomial struct{ coefficients []byte }

func makePolynomial(intercept byte, degree int, random io.Reader) (polynomial, error) {
	if degree < 1 || degree > 254 {
		return polynomial{}, fmt.Errorf("invalid polynomial degree %d", degree)
	}
	if random == nil {
		random = rand.Reader
	}
	p := polynomial{coefficients: make([]byte, degree+1)}
	p.coefficients[0] = intercept
	if _, err := io.ReadFull(random, p.coefficients[1:]); err != nil {
		return polynomial{}, fmt.Errorf("generate polynomial coefficients: %w", err)
	}
	return p, nil
}

func (p polynomial) evaluate(x byte) byte {
	if x == 0 {
		return p.coefficients[0]
	}
	out := p.coefficients[len(p.coefficients)-1]
	for i := len(p.coefficients) - 2; i >= 0; i-- {
		out = add(mult(out, x), p.coefficients[i])
	}
	return out
}

func interpolate(xs, ys []byte, x byte) byte {
	var result byte
	for i := range xs {
		basis := byte(1)
		for j := range xs {
			if i == j {
				continue
			}
			basis = mult(basis, div(add(x, xs[j]), add(xs[i], xs[j])))
		}
		result = add(result, mult(ys[i], basis))
	}
	return result
}

func div(a, b byte) byte {
	if b == 0 {
		panic("shamir: divide by zero")
	}
	ret := int(mult(a, inverse(b)))
	ret = subtle.ConstantTimeSelect(subtle.ConstantTimeByteEq(a, 0), 0, ret)
	return byte(ret)
}

func inverse(a byte) byte {
	b := mult(a, a)
	c := mult(a, b)
	b = mult(c, c)
	b = mult(b, b)
	c = mult(b, c)
	b = mult(b, b)
	b = mult(b, b)
	b = mult(b, c)
	b = mult(b, b)
	b = mult(a, b)
	return mult(b, b)
}

func mult(a, b byte) byte {
	var r byte
	for i := byte(8); i > 0; i-- {
		bit := i - 1
		r = (-(b>>bit&1) & a) ^ (-(r>>7) & 0x1B) ^ (r + r)
	}
	return r
}
func add(a, b byte) byte { return a ^ b }

func Split(secret []byte, parts, threshold int, random io.Reader) ([][]byte, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("cannot split an empty secret")
	}
	if threshold < 2 || threshold > 255 || parts < threshold || parts > 255 {
		return nil, fmt.Errorf("invalid Shamir parts/threshold")
	}
	if random == nil {
		random = rand.Reader
	}
	out := make([][]byte, parts)
	for i := range out {
		out[i] = make([]byte, len(secret)+1)
		out[i][len(secret)] = byte(i + 1)
	}
	for byteIndex, value := range secret {
		p, err := makePolynomial(value, threshold-1, random)
		if err != nil {
			return nil, err
		}
		for shareIndex := 0; shareIndex < parts; shareIndex++ {
			out[shareIndex][byteIndex] = p.evaluate(byte(shareIndex + 1))
		}
	}
	return out, nil
}

func Combine(parts [][]byte) ([]byte, error) {
	if len(parts) < 2 {
		return nil, fmt.Errorf("at least two shares are required")
	}
	partLen := len(parts[0])
	if partLen < 2 {
		return nil, fmt.Errorf("invalid share length")
	}
	for _, part := range parts[1:] {
		if len(part) != partLen {
			return nil, fmt.Errorf("all shares must have equal length")
		}
	}
	xs := make([]byte, len(parts))
	seen := make(map[byte]struct{}, len(parts))
	for i, part := range parts {
		x := part[partLen-1]
		if x == 0 {
			return nil, fmt.Errorf("share index must be non-zero")
		}
		if _, ok := seen[x]; ok {
			return nil, fmt.Errorf("duplicate share index")
		}
		seen[x] = struct{}{}
		xs[i] = x
	}
	secret := make([]byte, partLen-1)
	ys := make([]byte, len(parts))
	for byteIndex := range secret {
		for i, part := range parts {
			ys[i] = part[byteIndex]
		}
		secret[byteIndex] = interpolate(xs, ys, 0)
	}
	return secret, nil
}

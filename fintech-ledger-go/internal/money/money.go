// Package money represents currency as integer minor units (paise).
//
// float64 cannot exactly represent most decimal fractions (0.1 has no
// exact binary representation), so summing millions of float64 paise
// amounts accumulates rounding error that compounds over a transaction
// history. Paise is an int64 count of the smallest unit, so every
// operation is exact.
package money

import "fmt"

type Paise int64

func FromRupees(rupees, paise int64) Paise {
	return Paise(rupees*100 + paise)
}

func (p Paise) Add(o Paise) Paise { return p + o }
func (p Paise) Sub(o Paise) Paise { return p - o }
func (p Paise) Mul(n int64) Paise { return p * Paise(n) }

func (p Paise) IsNegative() bool { return p < 0 }

func (p Paise) String() string {
	sign := ""
	v := int64(p)
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s₹%d.%02d", sign, v/100, v%100)
}

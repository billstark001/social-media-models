package trajectory

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Precision controls only historical trajectory storage. Model and
// research checkpoint arithmetic remain float64.
type Precision struct {
	Opinions    string `json:"opinions"`
	OpinionSums string `json:"opinion_sums"`
}

func (p Precision) Resolved() Precision {
	if p.Opinions == "" {
		p.Opinions = "float32"
	}
	if p.OpinionSums == "" {
		p.OpinionSums = "float16"
	}
	return p
}

func (p Precision) Validate() error {
	for name, value := range map[string]string{"opinions": p.Opinions, "opinion_sums": p.OpinionSums} {
		if value != "" && value != "float16" && value != "float32" && value != "float64" {
			return fmt.Errorf("trajectory %s precision %q must be float16, float32, or float64", name, value)
		}
	}
	return nil
}

func precisionCode(name string) byte {
	switch name {
	case "float16":
		return 2
	case "float32":
		return 4
	case "float64":
		return 8
	default:
		panic("invalid trajectory precision")
	}
}

func precisionName(code byte) (string, error) {
	switch code {
	case 2:
		return "float16", nil
	case 4:
		return "float32", nil
	case 8:
		return "float64", nil
	default:
		return "", fmt.Errorf("invalid trajectory precision code %d", code)
	}
}

func putTrajectoryFloat(dst []byte, v float64, size byte) error {
	switch size {
	case 2:
		bits, err := float16Bits(v)
		if err != nil {
			return err
		}
		binary.LittleEndian.PutUint16(dst, bits)
	case 4:
		binary.LittleEndian.PutUint32(dst, math.Float32bits(float32(v)))
	case 8:
		binary.LittleEndian.PutUint64(dst, math.Float64bits(v))
	default:
		return fmt.Errorf("invalid trajectory float size %d", size)
	}
	return nil
}

func trajectoryFloat(src []byte, size byte) float64 {
	switch size {
	case 2:
		return float16Value(binary.LittleEndian.Uint16(src))
	case 4:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(src)))
	case 8:
		return math.Float64frombits(binary.LittleEndian.Uint64(src))
	default:
		panic("invalid trajectory float size")
	}
}

// float16Bits implements IEEE binary16 round-to-nearest, ties-to-even. Values
// outside its finite range fail rather than silently becoming infinity.
func float16Bits(v float64) (uint16, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 65504 {
		return 0, fmt.Errorf("value %g cannot be stored as finite float16", v)
	}
	u := math.Float64bits(v)
	sign := uint16(u>>48) & 0x8000
	exponent := int((u>>52)&0x7ff) - 1008
	mantissa := u & ((uint64(1) << 52) - 1)
	if exponent <= 0 {
		return sign | uint16(math.RoundToEven(math.Ldexp(math.Abs(v), 24))), nil
	}
	significand := (uint64(1) << 52) | mantissa
	base := significand >> 42
	remainder := significand & ((uint64(1) << 42) - 1)
	if remainder > 1<<41 || (remainder == 1<<41 && base&1 != 0) {
		base++
		if base == 0x800 {
			base = 0x400
			exponent++
		}
	}
	if exponent >= 31 {
		return 0, fmt.Errorf("value %g cannot be stored as finite float16", v)
	}
	return sign | uint16(exponent<<10) | uint16(base-0x400), nil
}

func float16Value(bits uint16) float64 {
	sign := 1.0
	if bits&0x8000 != 0 {
		sign = -1
	}
	exponent := int((bits >> 10) & 0x1f)
	mantissa := int(bits & 0x3ff)
	if exponent == 0 {
		return sign * math.Ldexp(float64(mantissa), -24)
	}
	if exponent == 31 {
		if mantissa == 0 {
			return sign * math.Inf(1)
		}
		return math.NaN()
	}
	return sign * math.Ldexp(float64(1024+mantissa), exponent-25)
}

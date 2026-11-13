package pyjson

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ReprFloat is Python's repr(float) with json.dumps' spelling of NaN and the infinities: shortest round-trip digits;
// scientific when the decimal exponent is below -4 or at least 16; always a ".0" or an exponent.
func ReprFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX, shortest
	mant, exp, _ := strings.Cut(s, "e")
	e, _ := strconv.Atoi(exp)
	neg := strings.HasPrefix(mant, "-")
	mant = strings.TrimPrefix(mant, "-")
	digits := strings.Replace(mant, ".", "", 1)
	var out string
	switch {
	case e < -4 || e >= 16:
		out = digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		out += fmt.Sprintf("e%+03d", e) // 1e+22, 1e-05, 1.5e+300: at least two exponent digits
	case e >= 0:
		for len(digits) <= e {
			digits += "0"
		}
		out = digits[:e+1] + "." + digits[e+1:]
		if strings.HasSuffix(out, ".") {
			out += "0"
		}
	default:
		out = "0." + strings.Repeat("0", -e-1) + digits
	}
	if neg {
		out = "-" + out
	}
	return out
}

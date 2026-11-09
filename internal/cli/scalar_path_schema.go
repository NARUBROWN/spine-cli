package cli

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

// scalarPathSchema는 고정 크기 정수의 범위를 손실 없이 표현합니다.
func scalarPathSchema(name string) map[string]any {
	switch name {
	case "bool":
		return map[string]any{"type": "boolean"}
	case "string":
		return map[string]any{"type": "string"}
	case "float32", "float64":
		return map[string]any{"type": "number"}
	}
	if name == "int" || name == "uint" || name == "uintptr" {
		return map[string]any{"type": "integer", "x-spine-platform-range-unresolved": true}
	}
	unsigned := strings.HasPrefix(name, "uint")
	bitsText := strings.TrimPrefix(name, "int")
	if unsigned {
		bitsText = strings.TrimPrefix(name, "uint")
	}
	bits, err := strconv.Atoi(bitsText)
	if err != nil {
		return unresolvedAPISchema(name)
	}
	max := new(big.Int).Lsh(big.NewInt(1), uint(bits))
	min := big.NewInt(0)
	if !unsigned {
		max.Rsh(max, 1)
		min.Neg(new(big.Int).Set(max))
	}
	max.Sub(max, big.NewInt(1))
	schema := map[string]any{"type": "integer", "minimum": json.Number(min.String()), "maximum": json.Number(max.String()), "x-spine-type-range": true, "x-spine-type-range-text": min.String() + " ~ " + max.String()}
	if !unsigned && (bits == 32 || bits == 64) {
		schema["format"] = "int" + bitsText
	}
	return schema
}

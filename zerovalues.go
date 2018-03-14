package genpgfuncs

import "strings"

func goZeroValueForType(t string) string {
	if strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "*") {
		return "nil"
	}
	if z, ok := zeroValuesForType[t]; ok {
		return z
	}
	return "" // t + "{}"
}

var zeroValuesForType = map[string]string{
	"bool":      "false",
	"string":    `""`,
	"float32":   "0",
	"float64":   "0",
	"int":       "0",
	"int16":     "0",
	"int32":     "0",
	"int64":     "0",
	"uint":      "0",
	"uint16":    "0",
	"uint32":    "0",
	"uint64":    "0",
	"time.Time": "time.Time{}",
	"uuid.UUID": "uuid.Nil",
}

package genpgfuncs

import (
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/domonda/go-types/uu"
)

var pgToGoType = map[string]string{
	"boolean":          "bool",
	"text":             "string",
	"varchar":          "string",
	"float4":           "float32",
	"real":             "float32",
	"float8":           "float64",
	"double precision": "float64",
	"smallint":         "int16",
	"int":              "int32",
	"integer":          "int32",
	"int4":             "int32",
	"bigint":           "int64",
	"int8":             "int64",
	"smallserial":      "int16",
	"serial":           "int32",
	"bigserial":        "int64",
	"date":             "time.Time",
	"timestamp":        "time.Time",
	"timestamptz":      "time.Time",
	"bytea":            "[]byte",
	"json":             "nullable.JSON",
	"jsonb":            "nullable.JSON",
	"uuid":             "uu.ID",
}

func PgToGoType(conn *sqlx.DB, t string, imports Imports, enums Enums, typeImport, typeMap map[string]string) string {
	slice := strings.HasSuffix(t, "[]")
	if slice {
		t = strings.TrimSuffix(t, "[]")
	} else if slice = strings.HasPrefix(t, "SETOF "); slice {
		t = strings.TrimPrefix(t, "SETOF ")
	}

	if goType, ok := typeMap[t]; ok {
		derefType := strings.TrimPrefix(goType, "*")
		if importPath, hasImport := typeImport[derefType]; hasImport {
			imports.Require(importPath)
		}
		if slice {
			goType = "[]" + goType
		}
		return goType
	}

	if goType, ok := pgToGoType[t]; ok {
		if importPath, hasImport := typeImport[goType]; hasImport {
			imports.Require(importPath)
		}
		if slice {
			goType = "[]" + goType
		}
		return goType
	}

	values, _ := GetEnumValues(conn, t)
	if len(values) > 0 {
		enum := Enum{
			Name:   t,
			Values: values,
		}
		enums[t] = enum
		goType := enum.GoName()
		if slice {
			goType = "[]" + goType
		}
		return goType
	}

	if slice {
		t = "[]" + t
	}
	return t
}

// TODO: replace with uu.IDSlice
func UUIDSliceToPgString(ids []uu.ID) string {
	if ids == nil {
		return "NULL"
	}

	var b strings.Builder
	b.WriteByte('{')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(id.String())
		b.WriteByte('"')
	}
	b.WriteByte('}')

	return b.String()
}

type packageType struct {
	Package  string
	TypeName string
}

var scanableTypes = map[string]packageType{
	"[]bool":    {"github.com/lib/pq", "pq.BoolArray"},
	"[]float64": {"github.com/lib/pq", "pq.Float64Array"},
	"[]int64":   {"github.com/lib/pq", "pq.Int64Array"},
	"[]string":  {"github.com/lib/pq", "pq.StringArray"},
}

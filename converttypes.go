package genpgfuncs

import (
	"strings"

	"github.com/jmoiron/sqlx"
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

	"uuid": "uuid.UUID",
}

var typeImport = map[string]string{
	"time.Time":    "time",
	"uuid.UUID":    "github.com/ungerik/go-uuid",
	"document.Doc": "github.com/domonda/Domonda/go/document",
}

func PgToGoType(db *sqlx.DB, t string, imports Imports, enums Enums, typeMap map[string]string) string {
	slice := strings.HasSuffix(t, "[]")
	if slice {
		t = strings.TrimSuffix(t, "[]")
	} else if slice = strings.HasPrefix(t, "SETOF "); slice {
		t = strings.TrimPrefix(t, "SETOF ")
	}

	if goType, ok := typeMap[t]; ok {
		derefType := strings.TrimPrefix(goType, "*")
		if imp, hasImport := typeImport[derefType]; hasImport {
			imports[imp] = struct{}{}
		}
		if slice {
			goType = "[]" + goType
		}
		return goType
	}

	if goType, ok := pgToGoType[t]; ok {
		if imp, hasImport := typeImport[goType]; hasImport {
			imports[imp] = struct{}{}
		}
		if slice {
			goType = "[]" + goType
		}
		return goType
	}

	values, _ := GetEnumValues(db, t)
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

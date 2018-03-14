package genpgfuncs

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
	dry "github.com/ungerik/go-dry"
)

type Enum struct {
	Name   string
	Values []string
}

func (enum *Enum) GoName() string {
	name := enum.Name
	p := strings.LastIndexByte(name, '.')
	name = name[p+1:]
	return dry.StringToUpperCamelCase(name)
}

func (enum *Enum) GoConstsAndValues() (constsAndValues []string) {
	baseName := enum.GoName()
	for _, enumValue := range enum.Values {
		constName := baseName + dry.StringToUpperCamelCase(enumValue)
		constsAndValues = append(constsAndValues, constName, enumValue)
	}
	return constsAndValues
}

func (enum *Enum) Fprint(writer io.Writer) {
	fmt.Fprintf(writer, "type %s string\n", enum.GoName())
	fmt.Fprint(writer, "const (\n")
	constsAndValues := enum.GoConstsAndValues()
	for i := 0; i < len(constsAndValues); i += 2 {
		name := constsAndValues[i]
		value := constsAndValues[i+1]
		fmt.Fprintf(writer, "%s %s = \"%s\" \n", name, enum.GoName(), value)
	}
	fmt.Fprint(writer, ")\n")

	fmt.Fprintf(writer, "func (c %s) Valid() bool {\n", enum.GoName())
	fmt.Fprint(writer, "switch c {\n")
	fmt.Fprint(writer, "case ")
	for i := 0; i < len(constsAndValues); i += 2 {
		name := constsAndValues[i]
		if i > 0 {
			fmt.Fprint(writer, ", ")
		}
		fmt.Fprint(writer, name)
	}
	fmt.Fprint(writer, ":\n")
	fmt.Fprint(writer, "return true\n")
	fmt.Fprint(writer, "}\n")
	fmt.Fprint(writer, "return false\n")
	fmt.Fprint(writer, "}\n")
}

type Enums map[string]Enum

func (enums Enums) Fprint(writer io.Writer) {
	sorted := make([]Enum, 0, len(enums))
	for _, enum := range enums {
		sorted = append(sorted, enum)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	for _, enum := range sorted {
		enum.Fprint(writer)
	}
}

func GetEnumValues(db *sqlx.DB, enum string) (values []string, err error) {
	const query = `
		SELECT e.enumlabel
			FROM pg_enum AS e
			JOIN pg_type t ON e.enumtypid = t.oid
			WHERE t.typname = $1`

	rows, err := db.Query(query, enum)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		err = rows.Scan(&value)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return values, nil
}

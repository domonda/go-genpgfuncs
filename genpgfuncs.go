package genpgfuncs

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/guregu/null"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
	dry "github.com/ungerik/go-dry"
	fs "github.com/ungerik/go-fs"
)

func IntrospectFunction(db *sqlx.DB, name string) (f *Function, err error) {
	// https://stackoverflow.com/questions/1347282/how-can-i-get-a-list-of-all-functions-stored-in-the-database-of-a-particular-sch
	const query = `
		SELECT
			pg_catalog.pg_get_function_arguments(p.oid) AS "Arguments",
			pg_catalog.pg_get_function_result(p.oid) AS "Result",
			CASE
				WHEN p.proisagg THEN 'agg'
				WHEN p.proiswindow THEN 'window'
				WHEN p.prorettype = 'pg_catalog.trigger'::pg_catalog.regtype THEN 'trigger'
				ELSE 'normal'
			END AS "Type",
			d.description
		FROM pg_catalog.pg_proc AS p
			LEFT JOIN pg_catalog.pg_namespace AS n ON n.oid = p.pronamespace
			LEFT JOIN pg_catalog.pg_description AS d ON d.objoid = p.oid
		WHERE pg_catalog.pg_function_is_visible(p.oid)
			AND n.nspname = $1
			AND p.proname = $2`

	namespace := "public"
	if p := strings.IndexRune(name, '.'); p != -1 {
		namespace = name[:p]
		name = name[p+1:]
	}

	var (
		arguments   string
		result      string
		kind        string
		description null.String
	)
	err = db.QueryRowx(query, namespace, name).Scan(&arguments, &result, &kind, &description)
	if err != nil {
		return nil, err
	}

	// fmt.Println(arguments, result, kind)

	f = &Function{
		Namespace:   namespace,
		Name:        name,
		Kind:        kind,
		Result:      result,
		Description: description.String,
	}
	for _, arg := range strings.Split(arguments, ",") {
		arg = strings.TrimSpace(arg)
		s := strings.IndexRune(arg, ' ')
		if s == -1 {
			return nil, errors.Errorf("Invalid type in argument: %s", arg)
		}
		f.Arguments = append(f.Arguments, FunctionArgument{Name: arg[:s], Type: arg[s+1:]})
	}

	return f, nil
}

type Imports map[string]struct{}

func (imports Imports) Fprint(writer io.Writer) {
	if len(imports) > 0 {
		fmt.Fprint(writer, "import (\n")
		for imp := range imports {
			fmt.Fprintf(writer, "\"%s\"\n", imp)
		}
		fmt.Fprint(writer, ")\n")
	}
}

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

type Function struct {
	Namespace   string
	Name        string
	Kind        string
	Arguments   []FunctionArgument
	Result      string
	Description string
}

type FunctionArgument struct {
	Name string
	Type string
}

func (arg *FunctionArgument) GoName() string {
	return dry.StringToLowerCamelCase(arg.Name)
}

func (arg *FunctionArgument) GoType(db *sqlx.DB, imports Imports, enums Enums, typeMap map[string]string) string {
	return PgToGoType(db, arg.Type, imports, enums, typeMap)
}

func GenerateNoResultFunctionsDBFirstArg(db *sqlx.DB, sourceFile, packageName string, typeMap map[string]string, argsDef bool, functions ...*Function) error {
	buf := bytes.NewBuffer(nil)
	imports := make(Imports)
	enums := make(Enums)

	for _, funcDef := range functions {
		imports["github.com/jmoiron/sqlx"] = struct{}{}

		fmt.Fprintf(buf, "func %s(db *sqlx.DB", dry.StringToUpperCamelCase(funcDef.Name))
		for _, arg := range funcDef.Arguments {
			fmt.Fprintf(buf, ", %s %s", dry.StringToLowerCamelCase(arg.Name), PgToGoType(db, arg.Type, imports, enums, typeMap))
		}
		fmt.Fprint(buf, ") error {\n")

		fmt.Fprintf(buf, "_, err := db.Exec(\"SELECT %s.%s(", funcDef.Namespace, funcDef.Name)
		for i := range funcDef.Arguments {
			if i > 0 {
				fmt.Fprint(buf, ", ")
			}
			fmt.Fprintf(buf, "$%d", i+1)
		}
		fmt.Fprint(buf, ")\"")
		for _, arg := range funcDef.Arguments {
			fmt.Fprintf(buf, ", %s", dry.StringToLowerCamelCase(arg.Name))
		}
		fmt.Fprint(buf, ")\n")
		fmt.Fprint(buf, "return err\n")
		fmt.Fprint(buf, "}\n\n")
	}

	file, err := fs.CleanFilePath(sourceFile).OpenWriter()
	if err != nil {
		return err
	}

	fmt.Fprintf(file, "package %s\n\n", packageName)
	imports.Fprint(file)
	enums.Fprint(file)

	_, err = file.Write(buf.Bytes())
	if err != nil {
		file.Close()
		return err
	}
	err = file.Close()
	if err != nil {
		return err
	}

	output, err := exec.Command("go", "fmt", sourceFile).CombinedOutput()
	if err != nil {
		fmt.Println(output)
		return err
	}

	fmt.Println("Generated file", sourceFile)

	return nil
}

func GenerateNoResultFunctions(db *sqlx.DB, sourceFile, packageName string, typeMap map[string]string, argsDef bool, functionNames ...string) (err error) {
	functions := make([]*Function, len(functionNames))
	for i, name := range functionNames {
		functions[i], err = IntrospectFunction(db, name)
		if err != nil {
			return err
		}
	}
	return generateNoResultFunctions(db, sourceFile, packageName, typeMap, argsDef, functions...)
}

func generateNoResultFunctions(db *sqlx.DB, sourceFile, packageName string, typeMap map[string]string, argsDef bool, functions ...*Function) error {
	buf := bytes.NewBuffer(nil)
	imports := make(Imports)
	enums := make(Enums)

	for _, funcDef := range functions {
		if argsDef {
			imports["github.com/ungerik/go-command"] = struct{}{}

			fmt.Fprintf(buf, "var %s struct {\ncommand.ArgsDef\n\n", dry.StringToUpperCamelCase(funcDef.Name)+"Args")
			for _, arg := range funcDef.Arguments {
				fmt.Fprintf(buf, "%s %s `arg:\"%s\"`\n", dry.StringToUpperCamelCase(arg.Name), arg.GoType(db, imports, enums, typeMap), arg.GoName())
			}
			fmt.Fprint(buf, "}\n\n")
		}

		fmt.Fprintf(buf, "func %s(", dry.StringToUpperCamelCase(funcDef.Name))
		for i, arg := range funcDef.Arguments {
			if i > 0 {
				fmt.Fprintf(buf, ", ")
			}
			fmt.Fprintf(buf, "%s %s", dry.StringToLowerCamelCase(arg.Name), PgToGoType(db, arg.Type, imports, enums, typeMap))
		}
		fmt.Fprint(buf, ") error {\n")

		fmt.Fprint(buf, "db, err := getDB()\nif err != nil {\nreturn err\n}\n")

		fmt.Fprintf(buf, "_, err = db.Exec(\"SELECT %s.%s(", funcDef.Namespace, funcDef.Name)
		for i := range funcDef.Arguments {
			if i > 0 {
				fmt.Fprint(buf, ", ")
			}
			fmt.Fprintf(buf, "$%d", i+1)
		}
		fmt.Fprint(buf, ")\"")
		for _, arg := range funcDef.Arguments {
			fmt.Fprintf(buf, ", %s", dry.StringToLowerCamelCase(arg.Name))
		}
		fmt.Fprint(buf, ")\n")
		fmt.Fprint(buf, "return err\n")
		fmt.Fprint(buf, "}\n\n")
	}

	file, err := fs.CleanFilePath(sourceFile).OpenWriter()
	if err != nil {
		return err
	}

	fmt.Fprintf(file, "package %s\n\n", packageName)
	imports.Fprint(file)
	enums.Fprint(file)

	_, err = file.Write(buf.Bytes())
	if err != nil {
		file.Close()
		return err
	}
	err = file.Close()
	if err != nil {
		return err
	}

	output, err := exec.Command("go", "fmt", sourceFile).CombinedOutput()
	if err != nil {
		fmt.Println(string(output))
		return err
	}

	fmt.Println("Generated file", sourceFile)

	return nil
}

func GenerateFunctions(db *sqlx.DB, sourceFile, packageName string, typeMap map[string]string, argsDef bool, functionNames ...string) (err error) {
	functions := make([]*Function, len(functionNames))
	for i, name := range functionNames {
		functions[i], err = IntrospectFunction(db, name)
		if err != nil {
			return err
		}
	}

	buf := bytes.NewBuffer(nil)
	imports := make(Imports)
	enums := make(Enums)

	for _, funcDef := range functions {
		goFuncName := dry.StringToUpperCamelCase(funcDef.Name)

		if argsDef {
			imports["github.com/ungerik/go-command"] = struct{}{}

			fmt.Fprintf(buf, "// %sArgs defines the arguments for %s\n", goFuncName, goFuncName)
			fmt.Fprintf(buf, "var %sArgs struct {\ncommand.ArgsDef\n\n", goFuncName)
			for _, arg := range funcDef.Arguments {
				fmt.Fprintf(buf, "%s %s `arg:\"%s\"`\n", dry.StringToUpperCamelCase(arg.Name), arg.GoType(db, imports, enums, typeMap), arg.GoName())
			}
			fmt.Fprint(buf, "}\n\n")
		}

		goResultType := ""
		hasResult := funcDef.Result != ""
		if hasResult {
			goResultType = PgToGoType(db, funcDef.Result, imports, enums, typeMap)
		}
		hasResultSlice := strings.HasPrefix(goResultType, "[]")

		if funcDef.Description != "" {
			desc := strings.ToLower(string(funcDef.Description[0])) + funcDef.Description[1:]
			for _, arg := range funcDef.Arguments {
				desc = strings.Replace(desc, arg.Name, arg.GoName(), -1)
			}
			fmt.Fprintf(buf, "// %s %s\n", goFuncName, desc)
		}
		fmt.Fprintf(buf, "func %s(", goFuncName)
		for i, arg := range funcDef.Arguments {
			if i > 0 {
				fmt.Fprintf(buf, ", ")
			}
			fmt.Fprintf(buf, "%s %s", arg.GoName(), arg.GoType(db, imports, enums, typeMap))
		}
		fmt.Fprint(buf, ")")

		if hasResult {
			zeroResultValue := goZeroValueForType(goResultType)
			if zeroResultValue == "" {
				zeroResultValue = "result"
			}
			fmt.Fprintf(buf, " (result %s, err error) {\n", goResultType)
			fmt.Fprintf(buf, "db, err := getDB()\nif err != nil {\nreturn %s, err\n}\n", zeroResultValue)
		} else {
			fmt.Fprint(buf, " error {\n")
			fmt.Fprint(buf, "db, err := getDB()\nif err != nil { return err }\n")
		}

		if hasResult {
			zeroResultValue := goZeroValueForType(goResultType)
			if zeroResultValue == "" {
				zeroResultValue = "result"
			}
			if hasResultSlice {
				fmt.Fprintf(buf, "rows, err := db.Query(\"SELECT %s.%s(", funcDef.Namespace, funcDef.Name)
				for i := range funcDef.Arguments {
					if i > 0 {
						fmt.Fprint(buf, ", ")
					}
					fmt.Fprintf(buf, "$%d", i+1)
				}
				fmt.Fprint(buf, ")\"")
				for _, arg := range funcDef.Arguments {
					fmt.Fprintf(buf, ", %s", arg.GoName())
				}
				fmt.Fprintf(buf, ")\nif err != nil { return %s, err }\ndefer rows.Close()\n", zeroResultValue)
				fmt.Fprint(buf, "for rows.Next() {\n")
				elemType := strings.TrimPrefix(goResultType, "[]")
				if strings.HasPrefix(elemType, "*") {
					fmt.Fprintf(buf, "value := new(%s)\n", strings.TrimPrefix(elemType, "*"))
					fmt.Fprintf(buf, "err = rows.Scan(value)\nif err != nil { return %s, err }\n", zeroResultValue)
				} else {
					fmt.Fprintf(buf, "var value %s\n", elemType)
					fmt.Fprintf(buf, "err = rows.Scan(&value)\nif err != nil { return %s, err }\n", zeroResultValue)
				}
				fmt.Fprint(buf, "result = append(result, value)\n}\n")
				fmt.Fprintf(buf, "if rows.Err() != nil { return %s, rows.Err() }\n", zeroResultValue)
			} else {
				fmt.Fprintf(buf, "err = db.QueryRow(\"SELECT %s.%s(", funcDef.Namespace, funcDef.Name)
				for i := range funcDef.Arguments {
					if i > 0 {
						fmt.Fprint(buf, ", ")
					}
					fmt.Fprintf(buf, "$%d", i+1)
				}
				fmt.Fprint(buf, ")\"")
				for _, arg := range funcDef.Arguments {
					fmt.Fprintf(buf, ", %s", arg.GoName())
				}
				fmt.Fprint(buf, ").Scan(&result)\n")
				if zeroResultValue != "" {
					fmt.Fprintf(buf, "if err != nil { return %s, err }\n", zeroResultValue)
				}
			}
			fmt.Fprint(buf, "return result, nil\n")
		} else {
			fmt.Fprintf(buf, "_, err = db.Exec(\"SELECT %s.%s(", funcDef.Namespace, funcDef.Name)
			for i := range funcDef.Arguments {
				if i > 0 {
					fmt.Fprint(buf, ", ")
				}
				fmt.Fprintf(buf, "$%d", i+1)
			}
			fmt.Fprint(buf, ")\"")
			for _, arg := range funcDef.Arguments {
				fmt.Fprintf(buf, ", %s", arg.GoName())
			}
			fmt.Fprint(buf, ")\n")
			fmt.Fprint(buf, "return err\n")
		}
		fmt.Fprint(buf, "}\n\n")
	}

	file, err := fs.CleanFilePath(sourceFile).OpenWriter()
	if err != nil {
		return err
	}

	fmt.Fprintf(file, "package %s\n\n", packageName)
	imports.Fprint(file)
	enums.Fprint(file)

	_, err = file.Write(buf.Bytes())
	if err != nil {
		file.Close()
		return err
	}
	err = file.Close()
	if err != nil {
		return err
	}

	output, err := exec.Command("go", "fmt", sourceFile).CombinedOutput()
	if err != nil {
		fmt.Println(string(output))
		return err
	}

	fmt.Println("Generated file", sourceFile)

	return nil
}

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

func goZeroValueForType(t string) string {
	if strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "*") {
		return "nil"
	}
	if z, ok := zeroValuesForType[t]; ok {
		return z
	}
	return "" // t + "{}"
}

package genpgfuncs

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/guregu/null"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
	dry "github.com/ungerik/go-dry"
	fs "github.com/ungerik/go-fs"
)

func IntrospectFunction(conn *sqlx.DB, name string) (f *Function, err error) {
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
	err = conn.QueryRow(query, namespace, name).Scan(&arguments, &result, &kind, &description)
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

func GenerateFunctions(conn *sqlx.DB, sourceFile, packageName string, typeMap map[string]string, argsDef bool, functionNames ...string) (err error) {
	functions := make([]*Function, len(functionNames))
	for i, name := range functionNames {
		functions[i], err = IntrospectFunction(conn, name)
		if err != nil {
			return err
		}
	}

	buf := bytes.NewBuffer(nil)
	imports := make(Imports)
	enums := make(Enums)

	for _, funcDef := range functions {
		goFuncName := dry.StringToUpperCamelCase(funcDef.Name)

		fmt.Println(funcDef.Namespace+"."+funcDef.Name, "=>", packageName+"."+goFuncName)

		if argsDef {
			imports["github.com/ungerik/go-command"] = struct{}{}

			fmt.Fprintf(buf, "// %sArgs defines the arguments for %s\n", goFuncName, goFuncName)
			fmt.Fprintf(buf, "var %sArgs struct {\ncommand.ArgsDef\n\n", goFuncName)
			for _, arg := range funcDef.Arguments {
				fmt.Fprintf(buf, "%s %s `arg:\"%s\"`\n", dry.StringToUpperCamelCase(arg.Name), arg.GoType(conn, imports, enums, typeMap), arg.GoName())
			}
			fmt.Fprint(buf, "}\n\n")
		}

		goResultType := ""
		hasResult := funcDef.Result != ""
		if hasResult {
			goResultType = PgToGoType(conn, funcDef.Result, imports, enums, typeMap)
		}
		hasResultSlice := strings.HasPrefix(goResultType, "[]")
		goResultTypeIsPointer := strings.HasPrefix(goResultType, "*")

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
			fmt.Fprintf(buf, "%s %s", arg.GoName(), arg.GoType(conn, imports, enums, typeMap))
		}
		fmt.Fprint(buf, ")")

		if hasResult {
			zeroResultValue := goZeroValueForType(goResultType)
			if zeroResultValue == "" {
				zeroResultValue = "result"
			}
			fmt.Fprintf(buf, " (result %s, err error) {\n", goResultType)
			fmt.Fprintf(buf, "conn, err := getConn()\nif err != nil {\nreturn %s, err\n}\n", zeroResultValue)
		} else {
			fmt.Fprint(buf, " error {\n")
			fmt.Fprint(buf, "conn, err := getConn()\nif err != nil { return err }\n")
		}

		if hasResult {
			zeroResultValue := goZeroValueForType(goResultType)
			if zeroResultValue == "" {
				zeroResultValue = "result"
			}
			if hasResultSlice {
				fmt.Fprintf(buf, "rows, err := conn.Query(\"SELECT %s.%s(", funcDef.Namespace, funcDef.Name)
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
				if goResultTypeIsPointer {
					fmt.Fprintf(buf, "result = new(%s)\n", strings.TrimPrefix(goResultType, "*"))
				}
				if goResultTypeIsPointer {
					fmt.Fprintf(buf, "err = conn.QueryRowx(\"SELECT * FROM %s.%s(", funcDef.Namespace, funcDef.Name)
				} else {
					fmt.Fprintf(buf, "err = conn.QueryRowx(\"SELECT %s.%s(", funcDef.Namespace, funcDef.Name)
				}
				for i := range funcDef.Arguments {
					if i > 0 {
						fmt.Fprint(buf, ", ")
					}
					fmt.Fprintf(buf, "$%d", i+1)
				}
				fmt.Fprint(buf, ")\"")
				for _, arg := range funcDef.Arguments {
					if arg.Type == "uuid[]" {
						imports["github.com/domonda/go-genpgfuncs"] = struct{}{}
						fmt.Fprintf(buf, ", genpgfuncs.UUIDSliceToPgString(%s)", arg.GoName())
					} else {
						fmt.Fprintf(buf, ", %s", arg.GoName())
					}
				}
				if goResultTypeIsPointer {
					fmt.Fprint(buf, ").StructScan(result)\n")
				} else {
					fmt.Fprint(buf, ").Scan(&result)\n")
				}
				if zeroResultValue != "" {
					fmt.Fprintf(buf, "if err != nil { return %s, err }\n", zeroResultValue)
				}
			}
			fmt.Fprint(buf, "return result, nil\n")
		} else {
			fmt.Fprintf(buf, "_, err = conn.Exec(\"SELECT %s.%s(", funcDef.Namespace, funcDef.Name)
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

func GenerateNoResultFunctionsDBFirstArg(conn *sqlx.DB, sourceFile, packageName string, typeMap map[string]string, argsDef bool, functions ...*Function) error {
	buf := bytes.NewBuffer(nil)
	imports := make(Imports)
	enums := make(Enums)

	for _, funcDef := range functions {
		imports["github.com/jmoiron/sqlx"] = struct{}{}

		fmt.Fprintf(buf, "func %s(conn *sqlx.DB", dry.StringToUpperCamelCase(funcDef.Name))
		for _, arg := range funcDef.Arguments {
			fmt.Fprintf(buf, ", %s %s", dry.StringToLowerCamelCase(arg.Name), PgToGoType(conn, arg.Type, imports, enums, typeMap))
		}
		fmt.Fprint(buf, ") error {\n")

		fmt.Fprintf(buf, "_, err := conn.Exec(\"SELECT %s.%s(", funcDef.Namespace, funcDef.Name)
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

func GenerateNoResultFunctions(conn *sqlx.DB, sourceFile, packageName string, typeMap map[string]string, argsDef bool, functionNames ...string) (err error) {
	functions := make([]*Function, len(functionNames))
	for i, name := range functionNames {
		functions[i], err = IntrospectFunction(conn, name)
		if err != nil {
			return err
		}
	}
	return generateNoResultFunctions(conn, sourceFile, packageName, typeMap, argsDef, functions...)
}

func generateNoResultFunctions(conn *sqlx.DB, sourceFile, packageName string, typeMap map[string]string, argsDef bool, functions ...*Function) error {
	buf := bytes.NewBuffer(nil)
	imports := make(Imports)
	enums := make(Enums)

	for _, funcDef := range functions {
		if argsDef {
			imports["github.com/ungerik/go-command"] = struct{}{}

			fmt.Fprintf(buf, "var %s struct {\ncommand.ArgsDef\n\n", dry.StringToUpperCamelCase(funcDef.Name)+"Args")
			for _, arg := range funcDef.Arguments {
				fmt.Fprintf(buf, "%s %s `arg:\"%s\"`\n", dry.StringToUpperCamelCase(arg.Name), arg.GoType(conn, imports, enums, typeMap), arg.GoName())
			}
			fmt.Fprint(buf, "}\n\n")
		}

		fmt.Fprintf(buf, "func %s(", dry.StringToUpperCamelCase(funcDef.Name))
		for i, arg := range funcDef.Arguments {
			if i > 0 {
				fmt.Fprintf(buf, ", ")
			}
			fmt.Fprintf(buf, "%s %s", dry.StringToLowerCamelCase(arg.Name), PgToGoType(conn, arg.Type, imports, enums, typeMap))
		}
		fmt.Fprint(buf, ") error {\n")

		fmt.Fprint(buf, "conn, err := getConn()\nif err != nil {\nreturn err\n}\n")

		fmt.Fprintf(buf, "_, err = conn.Exec(\"SELECT %s.%s(", funcDef.Namespace, funcDef.Name)
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

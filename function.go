package genpgfuncs

import (
	"github.com/jmoiron/sqlx"
)

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
	return unexportedGoName(arg.Name)
}

func (arg *FunctionArgument) GoType(conn *sqlx.DB, imports Imports, enums Enums, typeImport, typeMap map[string]string) string {
	return PgToGoType(conn, arg.Type, imports, enums, typeImport, typeMap)
}

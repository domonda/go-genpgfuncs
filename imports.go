package genpgfuncs

import (
	"fmt"
	"io"
	"sort"
)

type Imports map[string]struct{}

func (imports Imports) Require(importPath string) {
	imports[importPath] = struct{}{}
}

func (imports Imports) Fprint(writer io.Writer) {
	if len(imports) > 0 {
		var paths []string
		for importPath := range imports {
			paths = append(paths, importPath)
		}
		sort.Strings(paths)

		fmt.Fprint(writer, "import (\n")
		for _, importPath := range paths {
			fmt.Fprintf(writer, "\"%s\"\n", importPath)
		}
		fmt.Fprint(writer, ")\n")
	}
}

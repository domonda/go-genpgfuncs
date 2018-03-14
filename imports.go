package genpgfuncs

import (
	"fmt"
	"io"
)

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

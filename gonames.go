package genpgfuncs

import (
	"strings"

	dry "github.com/ungerik/go-dry"
)

var replaceSuffixes = [...][2]string{
	{"Id", "ID"},
	{"Uuid", "UUID"},
	{"Json", "JSON"},
	{"Xml", "XML"},
	{"Jpeg", "JPEG"},
	{"Jpg", "JPG"},
	{"Png", "PNG"},
	{"Svg", "SVG"},
}

func replaceSuffix(name string) string {
	for _, suffix := range replaceSuffixes {
		if strings.HasSuffix(name, suffix[0]) {
			return name[:len(name)-len(suffix[0])] + suffix[1]
		}
	}
	return name
}

func exportedGoName(name string) string {
	name = dry.StringToUpperCamelCase(name)
	name = replaceSuffix(name)
	return name
}

func unexportedGoName(name string) string {
	name = dry.StringToLowerCamelCase(name)
	name = replaceSuffix(name)
	return name
}
